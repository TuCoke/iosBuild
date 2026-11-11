// Runtime/app —— 任务执行体（与参考实现同名同路径）。
//
// 【为什么是这个形状】
// 参考实现的壳用 posix_spawn 拉起 Runtime/app，然后通过
//
//	GET http://127.0.0.1:8989/status
//	GET http://127.0.0.1:8989/task?cmd=…
//
// 跟它通信（这两个 URL 是从它壳的二进制里读出来的）。
// 我们照抄这个形状：同样的路径、同样的端口、同样的两个接口。
//
// 【为什么放在独立进程】
// 解释器可检查 context，但原生阻塞不能强制抢占；进程隔离供宿主超时终止兜底。
// 放到 Runtime/app 里，宿主超时可以直接 kill 掉再拉一个，App 本体不受影响。
// HTTP 与解释器入口可在其他系统编译检查；设备能力仍由 iOS+cgo 实现，其他系统返回不支持。
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"autogov1/v2/sdk/ios/script"
)

const listenAddr = "127.0.0.1:8989"

var (
	mu      sync.Mutex
	busy    bool
	started = time.Now()
	lastErr string
	runs    int
)

// reply 是 /task 的统一响应体（与宿主 sdk/ios/task 约定一致）。
type reply struct {
	OK        bool           `json:"ok"`
	Error     string         `json:"error,omitempty"`
	Logs      []string       `json:"logs,omitempty"`
	Result    map[string]any `json:"result,omitempty"`
	ElapsedMs int64          `json:"elapsedMs"`
}

func main() { runWithMainLoop(serve) }

func serve() {
	mux := http.NewServeMux()

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"pid":%d,"busy":%t,"runs":%d,"uptimeSec":%d,"addr":%q,"lastError":%q}`,
			os.Getpid(), busy, runs, int(time.Since(started).Seconds()), listenAddr, lastErr)
	})

	// /task?cmd=<base64(Go 源码)>
	//
	// 用 base64 而不是直接放源码：源码里有 & # 换行，URL 编码容易出错；
	// base64 是二进制的安全通道，解错也能立刻报出来。
	mux.HandleFunc("/task", func(w http.ResponseWriter, r *http.Request) {
		raw := r.URL.Query().Get("cmd")
		if raw == "" {
			writeReply(w, reply{Error: "缺少 cmd 参数"})
			return
		}
		source, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			writeReply(w, reply{Error: "cmd 不是合法 base64: " + err.Error()})
			return
		}

		mu.Lock()
		if busy {
			mu.Unlock()
			writeReply(w, reply{Error: "任务执行体正忙（同一时刻只跑一个任务）"})
			return
		}
		busy = true
		mu.Unlock()

		startedAt := time.Now()
		runner := script.New(script.Options{Timeout: 120 * time.Second})
		res, err := runner.Run(r.Context(), string(source))
		out := reply{Logs: res.Logs, ElapsedMs: time.Since(startedAt).Milliseconds()}
		if err != nil {
			out.Error = err.Error()
		} else {
			out.OK = true
		}
		if v, ok := res.Value.(map[string]any); ok {
			out.Result = v
		}

		mu.Lock()
		busy = false
		runs++
		if out.Error != "" {
			lastErr = out.Error
		}
		mu.Unlock()

		writeReply(w, out)
	})

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		// 端口被占多半是已有实例在跑 —— 直接退出，让宿主复用那个。
		fmt.Fprintf(os.Stderr, "监听 %s 失败: %v\n", listenAddr, err)
		os.Exit(2)
	}
	server := &http.Server{Handler: mux}
	if err := server.Serve(ln); err != nil {
		fmt.Fprintf(os.Stderr, "服务退出: %v\n", err)
		os.Exit(1)
	}
}

func writeReply(w http.ResponseWriter, r reply) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(r)
}
