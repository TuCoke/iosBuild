// Package task 通过 Runtime/app 执行脚本（与参考实现同一种做法）。
//
// 【参考实现的做法（从它的壳二进制里读出来的）】
//
//	posix_spawn Runtime/app
//	GET http://127.0.0.1:8989/status
//	GET http://127.0.0.1:8989/task?cmd=…
//
// 我们完全照抄：同样的可执行文件路径（Runtime/app）、同样的端口（8989）、
// 同样的两个接口。这样踩坑点会和它一致 —— 包括端口占用、进程残留、
// 启动竞态这些只有真的照做才会遇到的问题。
//
// 【为什么要独立进程】
// yaegi 无法抢占，脚本死循环会卡死宿主；放到 Runtime/app 里，超时就 kill 重拉。
package task

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"autogov1/v2/sdk/ios/internal/native"
)

// 与 Runtime/app 约定的地址（和参考实现一致）。
const (
	Addr        = "http://127.0.0.1:8989"
	statusURL   = Addr + "/status"
	taskURLBase = Addr + "/task"
)

// Result 是 /task 的返回。
type Result struct {
	OK        bool           `json:"ok"`
	Error     string         `json:"error"`
	Logs      []string       `json:"logs"`
	Result    map[string]any `json:"result"`
	ElapsedMs int64          `json:"elapsedMs"`
}

// Status 是 /status 的返回。
type Status struct {
	OK        bool   `json:"ok"`
	PID       int    `json:"pid"`
	Busy      bool   `json:"busy"`
	Runs      int    `json:"runs"`
	UptimeSec int    `json:"uptimeSec"`
	Addr      string `json:"addr"`
	LastError string `json:"lastError"`
	Running   bool   `json:"running"` // 本地补充：这次探测是否连通
}

// Options 控制一次任务。
type Options struct {
	Timeout time.Duration
}

// BundleBinary 返回 app 包内 Runtime/app 的路径（与参考实现同路径）。
func BundleBinary(ctx context.Context) string {
	var out struct {
		Path string `json:"path"`
	}
	askCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := native.Call(askCtx, "app.bundle_dir", nil, &out); err != nil || out.Path == "" {
		return ""
	}
	for _, candidate := range []string{
		filepath.Join(out.Path, "Runtime", "app"), // 参考实现的路径
		filepath.Join(out.Path, "Frameworks", "app"),
	} {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return ""
}

// StatusOf 探测 Runtime/app 是否在跑（HTTP 通了才算在）。
func StatusOf(ctx context.Context) Status {
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, statusURL, nil)
	if err != nil {
		return Status{}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Status{}
	}
	defer resp.Body.Close()
	var st Status
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return Status{}
	}
	st.Running = true
	return st
}

// EnsureRunning 确保 Runtime/app 在跑：没跑就 spawn 一个，然后等 /status 通。
//
// 这里有两个坑，照抄参考实现才会遇到：
//  1. spawn 成功 ≠ 端口在听 —— 必须轮询 /status，不能 spawn 完就发任务
//  2. 端口被占说明已有实例（上次没退干净），直接复用，不要再拉第二个
func EnsureRunning(ctx context.Context, timeout time.Duration) (Status, error) {
	if st := StatusOf(ctx); st.Running {
		return st, nil
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	binary := BundleBinary(ctx)
	if binary == "" {
		return Status{}, fmt.Errorf("找不到 Runtime/app（包内路径 Runtime/app）")
	}
	var spawned struct {
		PID int `json:"pid"`
	}
	spawnCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err := native.Call(spawnCtx, "utils.spawn", map[string]any{
		"path":    binary,
		"logPath": filepath.Join(os.TempDir(), "agov-runtime.log"),
	}, &spawned)
	cancel()
	if err != nil {
		return Status{}, fmt.Errorf("拉起 Runtime/app 失败: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st := StatusOf(ctx); st.Running {
			return st, nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return Status{}, fmt.Errorf("Runtime/app 已拉起但 %s 没有响应（可能是端口被占或启动失败）", statusURL)
}

// Run 把脚本交给 Runtime/app 执行。
//
// 与"写文件+等结果"不同：这里走 HTTP，与参考实现一致，
// 好处是宿主与执行体可以分别重启，不必共享文件句柄。
func Run(ctx context.Context, opts Options, source string) (Result, error) {
	if source == "" {
		return Result{Error: "脚本内容为空"}, fmt.Errorf("脚本内容为空")
	}
	if _, err := EnsureRunning(ctx, 10*time.Second); err != nil {
		return Result{Error: err.Error()}, err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	encoded := base64.StdEncoding.EncodeToString([]byte(source))
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, taskURLBase+"?cmd="+encoded, nil)
	if err != nil {
		return Result{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// 超时：把执行体杀掉，下次调用会自动重拉（隔离的意义就在这里）。
		if reqCtx.Err() == context.DeadlineExceeded {
			_ = Kill(ctx)
			return Result{Error: fmt.Sprintf("任务超时(%dms)，已终止 Runtime/app", timeout.Milliseconds())},
				fmt.Errorf("任务超时")
		}
		return Result{Error: "调用 Runtime/app 失败: " + err.Error()}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	var out Result
	if err := json.Unmarshal(body, &out); err != nil {
		return Result{Error: "结果解析失败: " + err.Error()}, err
	}
	return out, nil
}

// Kill 杀掉 Runtime/app（用 /status 拿到的 pid）。
func Kill(ctx context.Context) error {
	st := StatusOf(ctx)
	if !st.Running || st.PID <= 0 {
		return nil
	}
	var out struct {
		OK bool `json:"ok"`
	}
	killCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return native.Call(killCtx, "utils.kill", map[string]any{"pid": st.PID}, &out)
}
