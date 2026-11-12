//go:build ios && cgo

// ioshost 是给 iOS 宿主 App 用的 C ABI 入口。
//
// SDK 包含 Go 与原生桥（sdk/ios/...），直接被 Xcode 链接需要一个 c-archive：
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
	"strings"
	"sync"
	"time"
	"unsafe"

	"autogov1/v2/cmd/ioshost/debugapi"

	"autogov1/v2/sdk/ios/apkctl"
	"autogov1/v2/sdk/ios/app"
	"autogov1/v2/sdk/ios/capture"
	"autogov1/v2/sdk/ios/device"
	"autogov1/v2/sdk/ios/ime"
	"autogov1/v2/sdk/ios/motion"
	"autogov1/v2/sdk/ios/resources"
	"autogov1/v2/sdk/ios/script"
	"autogov1/v2/sdk/ios/script/api"
	"autogov1/v2/sdk/ios/script/worker"
	"autogov1/v2/sdk/ios/selftest"
	"autogov1/v2/sdk/ios/tunnel"
	"autogov1/v2/sdk/ios/types"
	"autogov1/v2/sdk/ios/update"
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
//  1. 隧道有东西可暴露（frpc 需要一个 localPort）
//  2. 服务端经隧道探活，确认设备在线（GET /status）
//
// 只监听 127.0.0.1，不监听 0.0.0.0 —— 避免局域网里的人直接连上来。
var (
	hostCallMu       sync.Mutex // 界面和局域网共用调用闸门，忙时拒绝，不叠加输入。
	debugMu          sync.Mutex
	debugServer      *debugapi.Server
	executionControl sync.RWMutex // SDK 安装与整个脚本执行互斥，保证子进程加载的版本稳定。
	localAPIMu       sync.Mutex
	localAPIServer   *http.Server
	localAPIPort     int
	localAPIStarted  time.Time
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
//
//export agov_ios_invoke
func agov_ios_invoke(method *C.char, payload *C.char) *C.char {
	if method == nil {
		return fail(contextError("缺少方法名"))
	}
	name := C.GoString(method)
	args := input(payload)
	duration := 30 * time.Second
	if name == "script.executeFile" {
		ms := num(args, "timeoutMs", 120000)
		if ms <= 0 || ms > 300000 {
			return fail(contextError("脚本期限必须在 1..300000 毫秒"))
		}
		duration = time.Duration(ms) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	if !hostCallMu.TryLock() {
		return fail(contextError("宿主正在执行其他操作，请稍后重试"))
	}
	defer hostCallMu.Unlock()
	return invokeSDK(ctx, name, args, duration)
}

// invokeSDK 复用 C ABI 的分派，不为 HTTP 再实现一套设备控制逻辑。
// 调用者持有 hostCallMu；独立 worker 在自己的进程内具有独立的锁。
func invokeSDK(ctx context.Context, name string, args map[string]any, duration time.Duration) *C.char {
	after := backgroundBefore(name)
	raw := invokeSDKBody(ctx, name, args, duration)
	if after == nil {
		return raw
	}
	var reply struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	success := raw != nil && json.Unmarshal([]byte(C.GoString(raw)), &reply) == nil && reply.OK && reply.Data["ok"] != false
	// 自动恢复中的隧道仍有后台运行需求，不能因一次启动观察失败释放 owner。
	if name == "tunnel.start" {
		if status, ok := reply.Data["status"].(map[string]any); ok && status["supervising"] == true {
			success = true
		}
	}
	after(success)
	return raw
}

func invokeSDKBody(ctx context.Context, name string, args map[string]any, duration time.Duration) *C.char {
	switch name {
	case "background.status":
		return okData(backgroundRequest("status", "", false))
	case "background.enable":
		value, ok := args["enabled"].(bool)
		if !ok {
			return fail(contextError("enabled 必须为布尔值"))
		}
		return okData(backgroundRequest("enable", "", value))
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

	case "runtime.diagnostics":
		report, err := selftest.Diagnostics(ctx)
		if err != nil {
			return fail(err)
		}
		return okData(report)
	case "runtime.diagnostics.worker":
		executionControl.RLock()
		defer executionControl.RUnlock()
		result, err := worker.Run(ctx, selftest.DiagnosticSource, "", map[string]any{})
		response := reply{OK: err == nil, Data: map[string]any{"processId": result.ProcessID, "elapsedMs": result.Elapsed.Milliseconds(), "logs": result.Logs, "result": result.Value}}
		if err != nil {
			response.Error = err.Error()
		}
		return encode(response)
	case "runtime.selftest.all":
		// 全量自检：逐个 op 真调一次，报告 可用/受环境限制/未实现/未验证
		return okData(selftest.All(ctx))

	case "runtime.selftest":
		v, err := device.SelfTest(ctx, num(args, "x", 0.5), num(args, "y", 0.5))
		if err != nil {
			return fail(err)
		}
		return okData(v)

	case "resources.status":
		report, err := resources.Inspect(ctx, args["verifyHash"] == true)
		if err != nil {
			return fail(err)
		}
		return okData(report)
	case "sdk.health":
		// 启动器仅探测运行时能否响应；不要求 AX、截图等受授权限制的能力可用。
		return okData(map[string]any{"ready": true, "protocol": 1})
	case "sdk.rollback":
		executionControl.Lock()
		defer executionControl.Unlock()
		state, err := update.Rollback(ctx)
		if err != nil {
			return fail(err)
		}
		return okData(map[string]any{"release": state, "needRestart": true})
	case "sdk.status":
		status, err := update.Status(ctx)
		if err != nil {
			return fail(err)
		}
		return okData(status)
	case "sdk.install":
		executionControl.Lock()
		defer executionControl.Unlock()
		raw, err := base64.StdEncoding.DecodeString(str(args, "base64"))
		if err != nil {
			return fail(err)
		}
		result, err := update.Install(ctx, raw, str(args, "sha256"))
		if err != nil {
			return fail(err)
		}
		return okData(result)
	case "apkctl.selftest":
		control := apkctl.NewSession(ctx)
		defer control.Close()
		value := control.Eval("selftest", "1+2")
		if value != "3" {
			return fail(contextError("宿主 JavaScriptCore 通道未返回 3"))
		}
		return okData(map[string]any{"result": value, "engine": "JavaScriptCore", "transport": "tcp-binary"})

	case "debugapi.start":
		debugMu.Lock()
		defer debugMu.Unlock()
		if debugServer == nil {
			paths, err := device.Paths(ctx)
			if err != nil {
				return fail(err)
			}
			server, err := debugapi.Start(debugapi.Config{Documents: paths.Documents, Video: debugapi.NewVideo(openBackgroundVideo), Invoke: func(callCtx context.Context, method string, parameters map[string]any) (json.RawMessage, error) {
				// 运行状态只读自己的锁；其他动作执行中也必须能查询 worker PID。
				if method != "script.status" && method != "background.status" {
					if !hostCallMu.TryLock() {
						return nil, contextError("宿主正在执行界面或脚本操作，请稍后重试")
					}
					defer hostCallMu.Unlock()
				}
				raw := invokeSDK(callCtx, method, parameters, 120*time.Second)
				defer agov_ios_free(raw)
				return json.RawMessage(C.GoString(raw)), nil
			}})
			if err != nil {
				return fail(err)
			}
			debugServer = server
		}
		addresses := []string{}
		if list, err := net.InterfaceAddrs(); err == nil {
			for _, addr := range list {
				if network, ok := addr.(*net.IPNet); ok && !network.IP.IsLoopback() && network.IP.To4() != nil {
					addresses = append(addresses, "http://"+network.IP.String()+":18990")
				}
			}
		}
		return okData(map[string]any{"port": 18990, "listen": "0.0.0.0:18990", "token": debugServer.Token(), "addresses": addresses, "mdns": "_agovhost._tcp.", "note": "固定调试 token；不要把调试端口暴露到公网"})
	case "debugapi.stop":
		debugMu.Lock()
		defer debugMu.Unlock()
		if debugServer != nil {
			if err := debugServer.Close(ctx); err != nil {
				return fail(err)
			}
			debugServer = nil
		}
		return okData(map[string]any{"stopped": true})
	case "localapi.start":
		port := int(num(args, "port", 18989))
		if err := startLocalAPI(port); err != nil {
			return fail(err)
		}
		return okData(map[string]any{"port": port, "url": fmt.Sprintf("http://127.0.0.1:%d/status", port)})
	case "localapi.stop":
		localAPIMu.Lock()
		defer localAPIMu.Unlock()
		if localAPIServer != nil {
			if err := localAPIServer.Shutdown(ctx); err != nil {
				return fail(err)
			}
			localAPIServer = nil
			localAPIPort = 0
		}
		return okData(map[string]any{"stopped": true})

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
		if !st.Running && !st.Supervising && st.LastError == "" {
			backgroundRequest("release", "tunnel", false)
		}
		return okData(map[string]any{"status": st, "logs": tunnel.Logs(30)})

	case "tunnel.logs":
		return okData(map[string]any{"logs": tunnel.Logs(int(num(args, "lines", 50)))})

	case "script.executeFile":
		// 仅供独立执行体在自己进程内调用；解释器和能力仍来自同一份动态 SDK。
		params, _ := args["params"].(map[string]any)
		runner := script.New(script.Options{Params: params, Timeout: duration, DeviceExclusive: true})
		res, err := runner.RunFile(ctx, str(args, "path"))
		out := reply{OK: err == nil, Data: map[string]any{"logs": res.Logs, "result": res.Value, "elapsedMs": res.Elapsed.Milliseconds()}}
		if err != nil {
			out.Error = err.Error()
		}
		return encode(out)
	case "script.run":
		executionControl.RLock()
		defer executionControl.RUnlock()
		source := str(args, "source")
		builtin := source == "" && str(args, "path") == ""
		if builtin {
			source = script.DemoSource
		}
		res, err := worker.Run(ctx, source, str(args, "path"), args)
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
			"processId": res.ProcessID,
		})

	case "script.status":
		return okData(worker.Status())
	case "script.remote":
		executionControl.RLock()
		defer executionControl.RUnlock()
		runner := script.New(script.Options{Params: args, Timeout: 45 * time.Second})
		res, err := runner.RunRemoteWithExecutor(ctx, script.RemoteOptions{
			URL:       str(args, "url"),
			SHA256:    str(args, "sha256"),
			CacheName: str(args, "name"),
		}, func(runCtx context.Context, source string) (script.Result, error) {
			return worker.Run(runCtx, source, "", args)
		})
		if err != nil {
			return okData(map[string]any{"ok": false, "error": err.Error(), "logs": res.Logs})
		}
		return okData(map[string]any{"ok": true, "logs": res.Logs, "result": res.Value,
			"elapsedMs": res.Elapsed.Milliseconds(), "processId": res.ProcessID})

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
	case "motion.gesture":
		raw, err := json.Marshal(args["frames"])
		var frames []types.Frame
		if err != nil || json.Unmarshal(raw, &frames) != nil || len(frames) == 0 {
			return fail(contextError("frames 必须是非空轨迹数组"))
		}
		if err = motion.Gesture(ctx, frames); err != nil {
			return fail(err)
		}
		return okData(map[string]any{"injected": "gesture"})
	case "motion.pinch":
		points := make([]types.Point, 4)
		for i, name := range []string{"a0", "b0", "a1", "b1"} {
			value, ok := args[name].(map[string]any)
			if !ok {
				return fail(contextError("缺少坐标 " + name))
			}
			x, xOK := value["x"].(float64)
			y, yOK := value["y"].(float64)
			if !xOK || !yOK {
				return fail(contextError("无效坐标 " + name))
			}
			points[i] = types.Point{X: x, Y: y}
		}
		if err := motion.Pinch(ctx, points[0], points[1], points[2], points[3], time.Duration(num(args, "durationMs", 500))*time.Millisecond); err != nil {
			return fail(err)
		}
		return okData(map[string]any{"injected": "pinch"})

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

	case "app.list":
		// 必须走显式分支：默认分支用 script/api 的 Session.Call，
		// 它的返回类型是 map[string]any，而 app.list 返回的是数组，
		// 真机实测会报 "cannot unmarshal array into Go value of type map[string]interface {}"。
		list, err := app.List(ctx)
		if err != nil {
			return fail(err)
		}
		return okData(list)

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
		// 复用现有脚本能力边界，避免每增加一个设备能力就再手写一个宿主分支。
		// 此通道只承载 JSON，截图二进制仍使用 capture.screenshot/capture.json。
		if name == "capture.image" {
			return fail(contextError("二进制截图请使用 capture.screenshot 或 capture.json"))
		}
		for _, prefix := range []string{"app.", "device.", "motion.", "ime.", "capture.", "uiacc.", "system.", "utils.", "hud.", "console.", "ppocr.", "yolo.", "dotocr.", "opencv.", "video."} {
			if strings.HasPrefix(name, prefix) {
				session, err := api.NewSession(ctx, nil)
				if err != nil {
					return fail(err)
				}
				data, err := session.CallJSON(name, args)
				if err != nil {
					return fail(err)
				}
				return okData(data)
			}
		}
		return fail(contextError("未知方法: " + name))
	}
}

// GoMain 是本项目动态库初始化入口，由宿主加载后调用一次。
// 样本的同名入口还会运行文件解释器；本项目将文件入口放在 Runtime/app。
//
//export GoMain
func GoMain() {
	_ = update.CaptureLoaded(context.Background())
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
