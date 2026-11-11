// Package debugapi 提供宿主局域网调试接口，不承担设备能力实现或云端业务。
package debugapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Invoke func(context.Context, string, map[string]any) (json.RawMessage, error)
type Config struct {
	Documents string
	Invoke    Invoke
}
type Server struct {
	cfg     Config
	token   string
	started time.Time
	http    *http.Server
	gate    chan struct{}
	mu      sync.Mutex
	results map[string]json.RawMessage
}

// 只允许已审查的调试能力，不暴露 shell、任意文件、卸载、重启、清数据或 SDK 安装。
var allowed = strings.Fields(`capabilities device.info device.display device.battery device.battery_status device.screen_on device.unlocked device.brightness_get device.memory device.ip device.vpn_status device.keep_awake device.brightness device.button device.lock device.wake device.clipget device.clipset app.foreground app.self app.list app.info app.installed app.bundle_path app.data_path app.bundle_dir app.icon app.launch app.open_url motion.tap motion.longpress motion.swipe motion.frame motion.configure motion.key ime.text ime.key capture.json uiacc.snapshot system.pid system.memory system.cpu resources.status sdk.status script.status tunnel.status apkctl.selftest`)
var routes = map[string]string{
	"/status": "device.info", "/capabilities": "capabilities", "/selftest": "runtime.selftest.all",
	"/diagnostics": "runtime.diagnostics", "/diagnostics/worker": "runtime.diagnostics.worker",
	"/screenshot": "capture.screenshot", "/ui-tree": "uiacc.snapshot", "/sdk": "sdk.status",
	"/resources": "resources.status", "/worker": "script.status", "/tunnel": "tunnel.status",
}

// Start 完成监听后才返回；不回退到随机端口，避免看似启动却连错服务。
func Start(cfg Config) (*Server, error) { return start(cfg, "0.0.0.0:18990") }
func start(cfg Config, addr string) (*Server, error) {
	if cfg.Invoke == nil || !filepath.IsAbs(cfg.Documents) {
		return nil, fmt.Errorf("调试服务缺少调用器或 Documents 绝对路径")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("调试端口监听失败: %w", err)
	}
	s := &Server{cfg: cfg, token: hex.EncodeToString(raw), started: time.Now(), gate: make(chan struct{}, 1), results: map[string]json.RawMessage{}}
	s.http = &http.Server{Addr: listener.Addr().String(), Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 125 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() { _ = s.http.Serve(listener) }()
	return s, nil
}

// Token 仅由宿主启动界面输出一次，不通过发现广播或未鉴权接口返回。
func (s *Server) Token() string                   { return s.token }
func (s *Server) Close(ctx context.Context) error { return s.http.Shutdown(ctx) }
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if len(token) != len(s.token) || subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
		failure(w, 401, "缺少或无效调试 token")
		return
	}
	// 不开放 CORS，也不接收网页跨源动作；命令行和直接访问链接不发送 Origin。
	if r.Header.Get("Origin") != "" {
		failure(w, 403, "调试接口不接受跨源网页请求")
		return
	}
	if r.Method != "GET" && !(r.Method == "POST" && (r.URL.Path == "/invoke" || r.URL.Path == "/script/test")) {
		w.Header().Set("Allow", "GET, POST")
		failure(w, 405, "请求方法不支持")
		return
	}
	switch r.URL.Path {
	case "/", "/api":
		writeJSON(w, 200, map[string]any{"ok": true, "data": map[string]any{"version": 1, "routes": routes, "extra": []string{"GET /health", "GET /log", "GET /results", "POST /invoke", "POST /script/test"}, "allowedMethods": allowed, "note": "GET /selftest 会触发自检，可能点击界面或改变剪贴板、悬浮层；只用于受控测试"}})
		return
	case "/health":
		writeJSON(w, 200, map[string]any{"ok": true, "data": map[string]any{"pid": os.Getpid(), "uptimeSec": int(time.Since(s.started).Seconds()), "port": 18990, "busy": len(s.gate) > 0}})
		return
	case "/log":
		s.log(w, r)
		return
	case "/results":
		s.mu.Lock()
		copy := map[string]json.RawMessage{}
		for k, v := range s.results {
			copy[k] = v
		}
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"ok": true, "data": copy})
		return
	}
	method, ok := routes[r.URL.Path]
	args := map[string]any{}
	if r.URL.Path == "/script/test" && r.Method == "POST" {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var empty struct{}
		if err := dec.Decode(&empty); err != nil && err != io.EOF {
			failure(w, 400, "内置脚本测试不接受源码或路径参数")
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			failure(w, 400, "请求只能为空或一个空 JSON 对象")
			return
		}
		method = "script.run"
		ok = true
	}
	if r.URL.Path == "/invoke" && r.Method == "POST" {
		var request struct {
			Method string         `json:"method"`
			Args   map[string]any `json:"args"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&request); err != nil {
			failure(w, 400, "无效 JSON 或请求超过 64 KiB")
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			failure(w, 400, "请求只能包含一个 JSON 对象")
			return
		}
		method = request.Method
		args = request.Args
		for _, name := range allowed {
			if name == method {
				ok = true
				break
			}
		}
		if !ok {
			failure(w, 403, "方法不在调试白名单；请查看 /api")
			return
		}
	}
	if !ok {
		failure(w, 404, "未知调试路径")
		return
	}
	if r.URL.Path == "/script/test" && r.Method != "POST" {
		failure(w, 405, "内置脚本测试请使用 POST")
		return
	}
	// 不把忙请求排成无限队列；取消请求不会提前释放尚未返回的原生调用。
	if method != "script.status" {
		select {
		case s.gate <- struct{}{}:
			defer func() { <-s.gate }()
		default:
			failure(w, 409, "已有调试任务正在执行")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	raw, err := s.cfg.Invoke(ctx, method, args)
	if err != nil {
		failure(w, 409, err.Error())
		return
	}
	if !json.Valid(raw) {
		failure(w, 502, "SDK 返回无效 JSON")
		return
	}
	// 保留有限数量的最近回执，不累积每一次截图或任务。
	s.mu.Lock()
	s.results[r.URL.Path] = append(json.RawMessage(nil), raw...)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write(raw)
}
func (s *Server) log(w http.ResponseWriter, r *http.Request) {
	// 固定文件名，客户端不能传任意路径；拒绝软链接，避免变成文件读取入口。
	path := filepath.Join(s.cfg.Documents, "app.log")
	info, err := os.Lstat(path)
	if err != nil {
		failure(w, 404, "app.log 尚不存在")
		return
	}
	if !info.Mode().IsRegular() {
		failure(w, 403, "日志不是普通文件")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		failure(w, 500, "无法打开日志")
		return
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		failure(w, 409, "日志刚被替换，请重试")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// 读取本次打开时的完整快照，后续追加不会让下载永远无法结束；支持 Range 下载。
	http.ServeContent(w, r, "app.log", opened.ModTime(), io.NewSectionReader(f, 0, opened.Size()))
}
func failure(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": message})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
