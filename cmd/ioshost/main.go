//go:build ios && cgo

// ioshost 是给 iOS 宿主 App 用的 C ABI 入口。
//
// SDK 本身是纯 Go 库（sdk/ios/...），直接被 Xcode 链接需要一个 c-archive：
//
//	GOOS=ios GOARCH=arm64 CGO_ENABLED=1 \
//	  go build -buildmode=c-archive -o host/ios/GoLib/libagovios.a ./cmd/ioshost
//
// 导出的两个函数（Swift/ObjC 侧用它们）：
//
//	char *agov_ios_invoke(const char *method, const char *payloadJson);
//	void  agov_ios_free(char *p);
//
// 约定：返回值由本库 malloc，调用方【必须】用 agov_ios_free 释放；返回 JSON：
//
//	{"ok":true,"data":...}  或  {"ok":false,"error":"..."}
package main

/*
#include <stdlib.h>
#include <stddef.h>

// 由同包的 ag_ax.m 实现（C 侧导出，壳也可以 dlsym）
int   AXBootstrap(void);
char *AXSweepNodesJSON(char *buf, size_t bufLen);
*/
import "C"

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
	"unsafe"

	"autogov1/v2/sdk/ios/app"
	"autogov1/v2/sdk/ios/capture"
	"autogov1/v2/sdk/ios/device"
	"autogov1/v2/sdk/ios/files"
	"autogov1/v2/sdk/ios/ime"
	"autogov1/v2/sdk/ios/motion"
	"autogov1/v2/sdk/ios/script"
	"autogov1/v2/sdk/ios/selftest"
	"autogov1/v2/sdk/ios/tunnel"
)

type reply struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func encode(r reply) *C.char {
	b, err := json.Marshal(r)
	if err != nil {
		// 连错误都序列化不了时给一个固定的可解析结果，绝不返回非法 JSON。
		b = []byte(`{"ok":false,"error":"结果序列化失败"}`)
	}
	return C.CString(string(b))
}

func fail(err error) *C.char {
	if err == nil {
		return encode(reply{OK: true})
	}
	return encode(reply{OK: false, Error: err.Error()})
}

func okData(v any) *C.char { return encode(reply{OK: true, Data: v}) }

func input(payload *C.char) map[string]any {
	out := map[string]any{}
	if payload == nil {
		return out
	}
	s := C.GoString(payload)
	if s == "" {
		return out
	}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func num(m map[string]any, key string, def float64) float64 {
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return def
}

func str(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func boolean(m map[string]any, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// ---- 本地 API ----
//
// 宿主在 127.0.0.1 上开一个小 HTTP 服务，两个用途：
//   1. 隧道有东西可暴露（frpc 需要一个 localPort）
//   2. 服务端经隧道探活，确认设备在线（GET /status）
//
// 只监听 127.0.0.1，不监听 0.0.0.0 —— 避免局域网里的人直接连上来。
var (
	localAPIMu      sync.Mutex
	localAPIServer  *http.Server
	localAPIPort    int
	localAPIStarted time.Time
)

func startLocalAPI(port int) error {
	if port <= 0 {
		port = 18989
	}
	localAPIMu.Lock()
	defer localAPIMu.Unlock()
	if localAPIServer != nil {
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"pid":%d,"port":%d,"uptimeSec":%d}`,
			os.Getpid(), port, int(time.Since(localAPIStarted).Seconds()))
	})
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "pong")
	})
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: mux}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("本地 API 监听失败: %w", err)
	}
	localAPIServer = server
	localAPIPort = port
	localAPIStarted = time.Now()
	go func() { _ = server.Serve(listener) }()
	return nil
}
// agov_ios_invoke 是壳唯一的调用入口：method + payloadJson → JSON 结果。
//
// 注意：//export 必须【紧邻】函数声明，cgo 才会生成导出符号。
// 之前它被上面新加的代码隔开了，结果归档里只有 agov_ios_free、没有 invoke，
// 表现就是 clang 报 <initial-undefines> / dlsym 失败 —— 踩过一次，别再挪。
//export agov_ios_invoke
func agov_ios_invoke(method *C.char, payload *C.char) *C.char {
	if method == nil {
		return fail(contextError("缺少方法名"))
	}
	name := C.GoString(method)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := input(payload)

	switch name {
	case "capabilities":
		v, err := device.Capabilities(ctx)
		if err != nil {
			return fail(err)
		}
		return okData(v)

	case "device.info":
		v, err := device.Info(ctx)
		if err != nil {
			return fail(err)
		}
		return okData(v)

	case "device.display":
		v, err := device.Display(ctx)
		if err != nil {
			return fail(err)
		}
		return okData(v)

	case "runtime.selftest.all":
		// 全量自检：逐个 op 真调一次，报告 可用/受环境限制/未实现/未验证
		return okData(selftest.All(ctx))

	case "runtime.selftest":
		v, err := device.SelfTest(ctx, num(args, "x", 0.5), num(args, "y", 0.5))
		if err != nil {
			return fail(err)
		}
		return okData(v)

	case "sdk.status":
		// 报告 SDK 来源：静态（编进主程序）还是动态（dylib，可热更）。
		dylib := files.Path("Plugins/libagovios.dylib")
		return okData(map[string]any{
			"pluginsDir":  files.Path("Plugins"),
			"dylibPath":   dylib,
			"dylibExists": files.Exists(dylib),
			"dylibBytes":  len(files.ReadBytes(dylib)),
		})

	case "sdk.install":
		// 热更入口：把下发的 dylib 落盘到沙盒 Plugins。
		// 已加载过动态库时 iOS 不允许卸载，需要重启进程才生效 —— 如实返回。
		b64 := str(args, "base64")
		if b64 == "" {
			return fail(contextError("缺少 base64"))
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return fail(err)
		}
		if len(raw) < 1024 {
			return fail(contextError("dylib 太小，不像有效产物"))
		}
		files.EnsureDir("Plugins")
		path := files.Path("Plugins/libagovios.dylib")
		files.WriteBytes(path, raw)
		if !files.Exists(path) {
			return fail(contextError("写入 dylib 失败: " + path))
		}
		return okData(map[string]any{"path": path, "bytes": len(raw), "needRestart": true})

	case "localapi.start":
		port := int(num(args, "port", 18989))
		if err := startLocalAPI(port); err != nil {
			return fail(err)
		}
		return okData(map[string]any{"port": port, "url": fmt.Sprintf("http://127.0.0.1:%d/status", port)})

	case "tunnel.start":
		cfg := tunnel.Config{
			Server:     str(args, "server"),
			ServerPort: int(num(args, "serverPort", 7000)),
			Token:      str(args, "token"),
			ProxyName:  str(args, "name"),
			LocalPort:  int(num(args, "localPort", 18989)),
			RemotePort: int(num(args, "remotePort", 0)),
			Type:       str(args, "type"),
			DisableTLS: boolean(args, "disableTls"),
		}
		st, err := tunnel.Start(ctx, cfg)
		if err != nil {
			return okData(map[string]any{"ok": false, "error": err.Error(), "status": st,
				"logs": tunnel.Logs(20)})
		}
		return okData(map[string]any{"ok": true, "status": st, "logs": tunnel.Logs(20)})

	case "tunnel.stop":
		st, err := tunnel.Stop(ctx)
		if err != nil {
			return okData(map[string]any{"ok": false, "error": err.Error(), "status": st})
		}
		return okData(map[string]any{"ok": true, "status": st})

	case "tunnel.status":
		st := tunnel.Query(ctx)
		return okData(map[string]any{"status": st, "logs": tunnel.Logs(30)})

	case "tunnel.logs":
		return okData(map[string]any{"logs": tunnel.Logs(int(num(args, "lines", 50)))})

	case "script.run":
		source := str(args, "source")
		builtin := source == ""
		if builtin {
			source = script.DemoSource
		}
		runner := script.New(script.Options{Params: args, Timeout: 45 * time.Second})
		res, err := runner.Run(ctx, source)
		if err != nil {
			// 失败也把脚本日志带回去：排错主要靠它。
			return okData(map[string]any{"ok": false, "error": err.Error(), "logs": res.Logs})
		}
		return okData(map[string]any{
			"ok":        true,
			"builtin":   builtin,
			"logs":      res.Logs,
			"result":    res.Value,
			"elapsedMs": res.Elapsed.Milliseconds(),
		})

	case "script.remote":
		runner := script.New(script.Options{Params: args, Timeout: 45 * time.Second})
		res, err := runner.RunRemote(ctx, script.RemoteOptions{
			URL:       str(args, "url"),
			SHA256:    str(args, "sha256"),
			CacheName: str(args, "name"),
		})
		if err != nil {
			return okData(map[string]any{"ok": false, "error": err.Error(), "logs": res.Logs})
		}
		return okData(map[string]any{"ok": true, "logs": res.Logs, "result": res.Value,
			"elapsedMs": res.Elapsed.Milliseconds()})

	case "motion.tap":
		if err := motion.Tap(ctx, num(args, "x", 0.5), num(args, "y", 0.5)); err != nil {
			return fail(err)
		}
		return okData(map[string]any{"injected": "tap"})

	case "motion.longpress":
		d := time.Duration(num(args, "durationMs", 600)) * time.Millisecond
		if err := motion.LongPress(ctx, num(args, "x", 0.5), num(args, "y", 0.5), d); err != nil {
			return fail(err)
		}
		return okData(map[string]any{"injected": "longpress", "durationMs": d.Milliseconds()})

	case "motion.swipe":
		d := time.Duration(num(args, "durationMs", 500)) * time.Millisecond
		if err := motion.SwipePath(ctx, num(args, "x1", 0.5), num(args, "y1", 0.7), num(args, "x2", 0.5), num(args, "y2", 0.3), d); err != nil {
			return fail(err)
		}
		return okData(map[string]any{"injected": "swipe", "durationMs": d.Milliseconds()})

	case "capture.screenshot":
		// 截图走二进制，这里转 base64 交给宿主显示；iOS 侧是 JPEG/PNG 编码后的数据。
		frame, err := capture.Screenshot(ctx, capture.Options{
			Rotation: int(num(args, "rotation", 0)),
			MaxSize:  int(num(args, "maxSize", 1024)),
			JPEG:     boolean(args, "jpeg"),
			Quality:  num(args, "quality", 0.8),
		})
		if err != nil {
			return fail(err)
		}
		return okData(map[string]any{
			"mime":   frame.MIME,
			"width":  frame.Width,
			"height": frame.Height,
			"base64": base64.StdEncoding.EncodeToString(frame.Data),
		})

	case "app.foreground":
		v, err := app.Foreground(ctx)
		if err != nil {
			return fail(err)
		}
		return okData(v)

	case "app.launch":
		if err := app.LaunchBundle(ctx, str(args, "bundleId")); err != nil {
			return fail(err)
		}
		return okData(map[string]any{"launched": str(args, "bundleId")})

	case "ime.text":
		if err := ime.InputTextContext(ctx, str(args, "text")); err != nil {
			return fail(err)
		}
		return okData(map[string]any{"typed": len(str(args, "text"))})

	default:
		return fail(contextError("未知方法: " + name))
	}
}

// GoMain 与参考实现同名：dylib 被 dlopen 之后由宿主/自身初始化调用一次，
// 用来确保原生能力就绪。参考实现里它是 void(void)，我们也保持一样。
//
//export GoMain
func GoMain() {
	// 幂等：这里只做"确保 AX 引导"，失败也不 panic —— 之后脚本调用时会再试一次。
	if C.AXBootstrap() != 0 {
		// 不可用是正常情况（普通签名/无授权），不阻断启动。
		println("[agov] AXBootstrap 未就绪（控件树不可用）")
	}
}

//export agov_ios_free
func agov_ios_free(p *C.char) {
	if p != nil {
		C.free(unsafe.Pointer(p))
	}
}

type stringError string

func (e stringError) Error() string { return string(e) }

func contextError(s string) error { return stringError(s) }

func main() {}
