// Package update 安装宿主使用的动态 SDK。加载中的 Go 库不原地卸载，更新在重启后生效。
package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/macho"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"autogov1/v2/sdk/ios/device"
)

const maxLibraryBytes = 128 << 20

var installMu sync.Mutex
var loadedOnce sync.Once
var loadedSnapshot map[string]any

// CaptureLoaded 在宿主刚加载 SDK 时保存文件摘要，不能在替换同名文件后把新文件摘要冒充已加载内存版本。
func CaptureLoaded(ctx context.Context) error {
	paths, err := device.Paths(ctx)
	if err != nil {
		return err
	}
	loadedOnce.Do(func() {
		loadedSnapshot = fileStatus(paths.LoadedImage)
		loadedSnapshot["capturedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	})
	return nil
}
func snapshotCopy() map[string]any {
	out := map[string]any{}
	for key, value := range loadedSnapshot {
		out[key] = value
	}
	return out
}
func runningBuild() map[string]any {
	out := map[string]any{}
	if info, ok := debug.ReadBuildInfo(); ok {
		out["goVersion"] = info.GoVersion
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision", "vcs.modified", "GOOS", "GOARCH":
				out[setting.Key] = setting.Value
			}
		}
	}
	return out
}

// Validate 验证实际 Mach-O 类型、CPU 和可选摘要；不将校验和当作系统签名授权。
func Validate(raw []byte, expected string) (string, error) {
	if len(raw) < 1024 || len(raw) > maxLibraryBytes {
		return "", fmt.Errorf("SDK 大小不在 1 KiB..128 MiB 范围内")
	}
	file, err := macho.NewFile(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("SDK 不是有效 Mach-O: %w", err)
	}
	defer file.Close()
	if file.Cpu != macho.CpuArm64 || file.Type != macho.TypeDylib {
		return "", fmt.Errorf("SDK 必须为 arm64 Mach-O 动态库")
	}
	sum := sha256.Sum256(raw)
	actual := hex.EncodeToString(sum[:])
	if expected != "" && !strings.EqualFold(expected, actual) {
		return "", fmt.Errorf("SDK SHA256 不符")
	}
	return actual, nil
}
func candidate(paths device.RuntimePaths) (string, error) {
	if paths.Documents == "" || !filepath.IsAbs(paths.Documents) {
		return "", fmt.Errorf("宿主未提供有效 Documents 目录")
	}
	return filepath.Join(paths.Documents, "Plugins", "libagovios.dylib"), nil
}
func Install(ctx context.Context, raw []byte, expected string) (map[string]any, error) {
	digest, err := Validate(raw, expected)
	if err != nil {
		return nil, err
	}
	if err = CaptureLoaded(ctx); err != nil {
		return nil, err
	}
	paths, err := device.Paths(ctx)
	if err != nil {
		return nil, err
	}
	filename, err := candidate(paths)
	if err != nil {
		return nil, err
	}
	installMu.Lock()
	defer installMu.Unlock()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return nil, err
	}
	temp, err := os.CreateTemp(filepath.Dir(filename), ".sdk-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(raw); err != nil {
		temp.Close()
		return nil, err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return nil, err
	}
	if err = temp.Close(); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = os.Rename(temp.Name(), filename); err != nil {
		return nil, err
	}
	return map[string]any{"path": filename, "bytes": len(raw), "sha256": digest, "loadedPath": paths.LoadedImage, "needRestart": true}, nil
}
func fileStatus(filename string) map[string]any {
	out := map[string]any{"path": filename, "exists": false}
	file, err := os.Open(filename)
	if err != nil {
		return out
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return out
	}
	out["exists"] = true
	out["bytes"] = info.Size()
	if info.Size() <= maxLibraryBytes {
		hash := sha256.New()
		if _, err = io.Copy(hash, file); err == nil {
			out["sha256"] = hex.EncodeToString(hash.Sum(nil))
		}
	}
	return out
}
func Status(ctx context.Context) (map[string]any, error) {
	if err := CaptureLoaded(ctx); err != nil {
		return nil, err
	}
	paths, err := device.Paths(ctx)
	if err != nil {
		return nil, err
	}
	filename, err := candidate(paths)
	if err != nil {
		return nil, err
	}
	installed := fileStatus(filename)
	loaded := snapshotCopy()
	pending := installed["exists"] == true && installed["sha256"] != loaded["sha256"]
	return map[string]any{"loaded": loaded, "loadedFileNow": fileStatus(paths.LoadedImage), "candidate": installed, "runningBuild": runningBuild(), "pluginsDir": filepath.Dir(filename), "pendingRestart": pending, "restartRequiredForInstall": true}, nil
}
