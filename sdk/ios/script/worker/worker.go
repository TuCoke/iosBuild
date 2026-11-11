// Package worker 由宿主通过 posix_spawn 运行独立的 Go/ZIP 脚本进程。
// 文件执行与参考样本入口一致；结果文件和参数文件属于本项目的宿主约定。
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"autogov1/v2/sdk/ios/device"
	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/script"
	"autogov1/v2/sdk/ios/types"
	"autogov1/v2/sdk/ios/update"
)

// 同一设备同一时刻只启动一个工作进程，防止不同解释器同时写入设备。
var gate = make(chan struct{}, 1)
var state struct {
	sync.Mutex
	PID       int
	Started   time.Time
	Directory string
}

type Reply struct {
	ProcessID int      `json:"processId"`
	OK        bool     `json:"ok"`
	Error     string   `json:"error,omitempty"`
	Logs      []string `json:"logs,omitempty"`
	Value     any      `json:"result,omitempty"`
	ElapsedMS int64    `json:"elapsedMs"`
}

func Status() map[string]any {
	state.Lock()
	defer state.Unlock()
	return map[string]any{"pid": state.PID, "running": state.PID > 0, "started": state.Started}
}
func Run(ctx context.Context, source, filename string, params map[string]any) (script.Result, error) {
	if ctx == nil {
		return script.Result{}, fmt.Errorf("脚本 context 不能为空")
	}
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return script.Result{}, ctx.Err()
	}
	// 上一次终止结果未知时保留隔离状态；确认旧进程退出前不得启动下一份脚本。
	state.Lock()
	previous, previousDir := state.PID, state.Directory
	state.Unlock()
	if previous > 0 {
		var wait struct {
			Finished bool `json:"finished"`
		}
		if err := native.Call(ctx, "utils.wait", map[string]any{"pid": previous}, &wait); err != nil || !wait.Finished {
			return script.Result{}, fmt.Errorf("%w: 上一工作进程 %d 尚未确认退出", types.ErrBusy, previous)
		}
		_ = os.RemoveAll(previousDir)
		state.Lock()
		state.PID = 0
		state.Directory = ""
		state.Unlock()
	}
	paths, err := device.Paths(ctx)
	if err != nil {
		return script.Result{}, err
	}
	dir, err := os.MkdirTemp(filepath.Join(paths.Documents), "agov-run-*")
	if err != nil {
		return script.Result{}, err
	}
	// 只有确认退出后才删除任务文件，避免进程仍在写而宿主提前回收现场。
	clean := true
	defer func() {
		if clean {
			os.RemoveAll(dir)
		}
	}()
	if filename == "" {
		if source == "" || len(source) > 4<<20 {
			return script.Result{}, fmt.Errorf("脚本为空或超过 4 MiB")
		}
		filename = filepath.Join(dir, "main.go")
		if err = os.WriteFile(filename, []byte(source), 0600); err != nil {
			return script.Result{}, err
		}
	} else {
		filename, err = filepath.Abs(filename)
		if err != nil {
			return script.Result{}, err
		}
	}
	if params == nil {
		params = map[string]any{}
	}
	parameters, err := json.Marshal(params)
	if err != nil {
		return script.Result{}, err
	}
	if len(parameters) > 4<<20 {
		return script.Result{}, fmt.Errorf("脚本参数超过 4 MiB")
	}
	paramsPath := filepath.Join(dir, "params.json")
	if err = os.WriteFile(paramsPath, parameters, 0600); err != nil {
		return script.Result{}, err
	}
	resultPath := filepath.Join(dir, "result.json")
	timeout := 120 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	if timeout <= 0 {
		return script.Result{}, ctx.Err()
	}
	// 【为什么不再直接对包内 Runtime/app 做 chmod】
	// 真机日志实测（iPhone 11 / iOS 16.5.1）：
	//   chmod /private/var/containers/Bundle/Application/…/AgovHost.app/Runtime/app:
	//   operation not permitted
	// App 包在设备上是只读挂载的，运行期 chmod 必然失败；模拟器的 App 包可写，
	// 所以这条只在真机暴露。做法：复制到沙盒可写目录（Documents/Worker）再 chmod。
	binary, err := prepareWorkerBinary(paths)
	if err != nil {
		return script.Result{}, err
	}
	sdkLibrary, err := update.ActiveLibrary(ctx)
	if err != nil {
		return script.Result{}, err
	}
	var launched struct {
		PID int `json:"pid"`
	}
	err = native.Call(ctx, "utils.spawn", map[string]any{"path": binary, "args": []string{"--result", resultPath, "--params", paramsPath, "--sdk", sdkLibrary, "--timeout-ms", fmt.Sprint(timeout.Milliseconds()), filename}, "logPath": filepath.Join(dir, "worker.log")}, &launched)
	if err != nil {
		return script.Result{}, err
	}
	pid := launched.PID
	if pid <= 0 {
		return script.Result{}, fmt.Errorf("工作进程未返回有效 PID")
	}
	state.Lock()
	state.PID = pid
	state.Started = time.Now()
	state.Directory = dir
	state.Unlock()
	defer func() {
		if clean {
			state.Lock()
			state.PID = 0
			state.Directory = ""
			state.Unlock()
		}
	}()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		var wait struct {
			Finished bool `json:"finished"`
			Code     int  `json:"code"`
		}
		if err = native.Call(ctx, "utils.wait", map[string]any{"pid": pid}, &wait); err == nil && wait.Finished {
			body, readErr := os.ReadFile(resultPath)
			if readErr != nil {
				return script.Result{}, fmt.Errorf("工作进程退出(code=%d)，没有完整结果: %w", wait.Code, readErr)
			}
			var reply Reply
			if err = json.Unmarshal(body, &reply); err != nil {
				return script.Result{}, err
			}
			if reply.ProcessID != pid {
				return script.Result{}, fmt.Errorf("工作进程结果 PID 不匹配: 期望 %d，实际 %d", pid, reply.ProcessID)
			}
			res := script.Result{ProcessID: pid, Logs: reply.Logs, Value: reply.Value, Elapsed: time.Duration(reply.ElapsedMS) * time.Millisecond}
			if !reply.OK || wait.Code != 0 {
				return res, fmt.Errorf("脚本工作进程失败(code=%d): %s", wait.Code, reply.Error)
			}
			return res, nil
		}
		if err != nil && ctx.Err() == nil {
			return script.Result{}, stopWorker(pid, dir, &clean, err)
		}
		select {
		case <-ctx.Done():
			return script.Result{}, stopWorker(pid, dir, &clean, ctx.Err())
		case <-ticker.C:
		}
	}
}
func stopWorker(pid int, dir string, clean *bool, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := native.Call(ctx, "utils.kill", map[string]any{"pid": pid}, nil)
	if err != nil {
		*clean = false
		return fmt.Errorf("%w；未确认工作进程退出，保留现场 %s: %v", cause, dir, err)
	}
	return cause
}

// prepareWorkerBinary 把包内的 Runtime/app 复制到沙盒可写目录，并在那里设置执行位。
//
// 【为什么必须复制】
// 设备上的 App 包是只读挂载，运行期 chmod 一定 operation not permitted（真机实测）。
// Documents/Worker 属于 App 自己，chmod 才能成功。
//
// 【为什么先比对大小】
// 工作进程二进制十几 MB，每次跑脚本都拷一遍没必要；大小一致就复用，
// 只重设权限。内容不一致（SDK 升级后）才重新写。
func prepareWorkerBinary(paths device.RuntimePaths) (string, error) {
	src := filepath.Join(paths.Bundle, "Runtime", "app")
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("读取工作进程失败: %w", err)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("工作进程二进制为空: %s", src)
	}
	dir := filepath.Join(paths.Documents, "Worker")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("创建工作进程目录失败: %w", err)
	}
	dst := filepath.Join(dir, "app")
	if old, err := os.ReadFile(dst); err == nil && len(old) == len(data) {
		if err := os.Chmod(dst, 0755); err == nil {
			// 二进制没变也要确保依赖库在位（用户可能手动清过 Documents）
			copyWorkerDylibs(paths.Bundle, dir)
			return dst, nil
		}
	}
	if err := os.WriteFile(dst, data, 0755); err != nil {
		return "", fmt.Errorf("写入工作进程失败: %w", err)
	}
	if err := os.Chmod(dst, 0755); err != nil {
		return "", fmt.Errorf("设置工作进程执行权限失败: %w", err)
	}
	// 【必须把依赖库一起搬过来】
	// 真机日志实测：工作进程从 Documents/Worker 启动后
	//   dlopen(bundle/libagovios.dylib) → "Library not loaded: @rpath/libimgui.dylib"
	// 原因：libagovios.dylib 的 install_name 是 @rpath/libimgui.dylib，
	//      dyld 只用【可执行文件】的 rpath 解析（这里是 Documents/Worker），
	//      而 libimgui.dylib 在 App 包根目录，找不到。
	// 处理：把包里会被 @rpath 引用的 dylib 复制到工作进程同目录。
	copyWorkerDylibs(paths.Bundle, dir)
	return dst, nil
}

// copyWorkerDylibs 把 App 包里会被 @rpath 引用的动态库复制到工作进程目录。
//
// 目前是 libimgui.dylib（libagovios.dylib 的依赖）。
// 识别栈的 libppocr/libncnn/libopencv_* 由 ag_rec 用绝对路径 dlopen，
// 不经过 @rpath，所以不在这里搬。
// 复制失败不致命：工作进程会给出明确的 dlopen 错误，便于排查。
func copyWorkerDylibs(bundleDir, workerDir string) {
	for _, name := range []string{"libimgui.dylib"} {
		data, err := os.ReadFile(filepath.Join(bundleDir, name))
		if err != nil || len(data) == 0 {
			continue
		}
		dst := filepath.Join(workerDir, name)
		if old, err := os.ReadFile(dst); err == nil && len(old) == len(data) {
			_ = os.Chmod(dst, 0755)
			continue
		}
		if err := os.WriteFile(dst, data, 0755); err == nil {
			_ = os.Chmod(dst, 0755)
		}
	}
}