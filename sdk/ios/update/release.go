package update

import (
	"bytes"
	"context"
	"debug/macho"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"autogov1/v2/sdk/ios/device"
)

// ReleaseState 与 ObjC 启动器共享。版本目录不可变，运行中的脚本始终加载原版本。
// Attempt 只由启动器设置；未完成健康确认就退出时，下次启动回退到 Current。
type ReleaseState struct {
	Version   int    `json:"version"`
	Current   string `json:"current,omitempty"`
	Previous  string `json:"previous,omitempty"`
	Pending   string `json:"pending,omitempty"`
	Attempt   string `json:"attempt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	LastError string `json:"lastError,omitempty"`
}

// Platform 检查 Mach-O 的目标平台。arm64 不能用于区分真机与模拟器。
func Platform(raw []byte) (string, error) {
	file, err := macho.NewFile(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	defer file.Close()
	legacy := false
	for _, load := range file.Loads {
		data := load.Raw()
		if len(data) < 8 {
			continue
		}
		cmd := file.ByteOrder.Uint32(data)
		switch cmd {
		case 0x32: // LC_BUILD_VERSION
			if len(data) < 24 {
				return "", fmt.Errorf("SDK 平台载荷不完整")
			}
			switch file.ByteOrder.Uint32(data[8:12]) {
			case 2:
				return "ios", nil
			case 7:
				return "ios-simulator", nil
			default:
				return "", fmt.Errorf("SDK 不是 iOS 或 iOS 模拟器动态库")
			}
		case 0x25: // LC_VERSION_MIN_IPHONEOS：仅旧格式真机库允许此标记。
			if len(data) < 16 {
				return "", fmt.Errorf("SDK 最低系统载荷不完整")
			}
			legacy = true
		}
	}
	if legacy {
		return "ios", nil
	}
	return "", fmt.Errorf("SDK 没有可识别的平台信息")
}

func readState(dir string) (ReleaseState, error) {
	state := ReleaseState{Version: 1}
	raw, err := os.ReadFile(filepath.Join(dir, "release.json"))
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err = json.Unmarshal(raw, &state); err != nil {
		return state, fmt.Errorf("SDK 版本状态损坏: %w", err)
	}
	if state.Version != 1 {
		return state, fmt.Errorf("SDK 版本状态格式不支持")
	}
	for _, digest := range []string{state.Current, state.Previous, state.Pending, state.Attempt} {
		if digest != "" && !validDigest(digest) {
			return state, fmt.Errorf("SDK 版本摘要无效")
		}
	}
	return state, nil
}
func validDigest(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	for _, c := range digest {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func writeState(dir string, state ReleaseState) error {
	state.Version = 1
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, "release.json"), raw, 0600)
}
func atomicWrite(filename string, raw []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(filename), ".install-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filename)
}
func versionPath(dir, digest string) string {
	return filepath.Join(dir, "versions", digest, "libagovios.dylib")
}

// Rollback 只安排下次启动使用已确认版本；禁止在同一进程卸载并加载第二个 Go runtime。
func Rollback(ctx context.Context) (ReleaseState, error) {
	paths, err := device.Paths(ctx)
	if err != nil {
		return ReleaseState{}, err
	}
	path, err := candidate(paths)
	if err != nil {
		return ReleaseState{}, err
	}
	installMu.Lock()
	defer installMu.Unlock()
	dir := filepath.Dir(path)
	state, err := readState(dir)
	if err != nil {
		return state, err
	}
	if state.Attempt != "" {
		return state, fmt.Errorf("SDK 正在启动确认，暂不能修改版本")
	}
	if state.Pending != "" {
		state.Pending = ""
	} else {
		if state.Previous == "" {
			state.Current = ""
		} else {
			state.Pending = state.Previous
		}
	}
	state.LastError = "用户安排回退，重启后生效"
	return state, writeState(dir, state)
}
