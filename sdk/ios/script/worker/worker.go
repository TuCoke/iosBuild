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
)

// 同一设备同一时刻只启动一个工作进程，防止不同解释器同时写入设备。
var gate = make(chan struct{}, 1)
var state struct {
	sync.Mutex
	PID     int
	Started time.Time
}

type Reply struct {
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
	var launched struct {
		PID int `json:"pid"`
	}
	err = native.Call(ctx, "utils.spawn", map[string]any{"path": filepath.Join(paths.Bundle, "Runtime", "app"), "args": []string{"--result", resultPath, "--params", paramsPath, "--timeout", timeout.String(), filename}, "logPath": filepath.Join(dir, "worker.log")}, &launched)
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
	state.Unlock()
	defer func() { state.Lock(); state.PID = 0; state.Unlock() }()
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
			res := script.Result{Logs: reply.Logs, Value: reply.Value, Elapsed: time.Duration(reply.ElapsedMS) * time.Millisecond}
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
