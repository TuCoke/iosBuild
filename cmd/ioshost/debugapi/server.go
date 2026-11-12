// Package debugapi 提供宿主局域网调试接口，不承担设备能力实现或云端业务。
package debugapi

import (
	"context"
	"crypto/subtle"
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
	Video     *Video
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
// tunnel.start/stop/logs 用于真机联调：入站连接被 iOS 限制，必须能远程拉起 frpc 验证穿透。
var allowed = strings.Fields(`background.status background.enable localapi.stop capabilities device.info device.display device.battery device.battery_status device.screen_on device.unlocked device.brightness_get device.memory device.ip device.wifi_mac device.vpn_status device.keep_awake device.brightness device.button device.lock device.wake device.clipget device.clipset app.foreground app.self app.list app.list_all app.info app.installed app.bundle_path app.data_path app.bundle_dir app.icon app.launch app.stop app.open_url motion.gesture motion.pinch motion.tap motion.longpress motion.swipe motion.frame motion.pointer motion.status motion.configure motion.key ime.text ime.segments ime.key capture.screenshot capture.json capture.release uiacc.snapshot uiacc.warmup uiacc.prepare uiacc.restore system.pid system.memory system.cpu utils.toast utils.alert utils.input_alert hud.show hud.visible hud.hide hud.destroy console.show console.print console.clear console.hide console.visible console.destroy ppocr.load ppocr.ocr ppocr.close yolo.load yolo.set_image yolo.detect yolo.detect_image yolo.close dotocr.set_dict dotocr.ocr dotocr.find opencv.decode opencv.rgba opencv.info opencv.encode opencv.match opencv.clone opencv.gray opencv.crop opencv.resize opencv.rotate opencv.threshold opencv.blur opencv.canny opencv.close resources.status sdk.status sdk.health script.status tunnel.status tunnel.start tunnel.stop tunnel.logs apkctl.selftest video.start video.read video.status video.keyframe video.bitrate video.stop`)
var routes = map[string]string{
	"/background": "background.status",
	"/sdk/health": "sdk.health",
	"/status":     "device.info", "/capabilities": "capabilities", "/selftest": "runtime.selftest.all",
	"/diagnostics": "runtime.diagnostics", "/diagnostics/worker": "runtime.diagnostics.worker",
	"/screenshot": "capture.screenshot", "/ui-tree": "uiacc.snapshot", "/sdk": "sdk.status",
	"/resources": "resources.status", "/worker": "script.status", "/tunnel": "tunnel.status",
}

var videoRoutes = map[string]string{"/video/start": "video.start", "/video/packet": "video.read", "/video/status": "video.status", "/video/keyframe": "video.keyframe", "/video/bitrate": "video.bitrate", "/video/stop": "video.stop"}

// Start 完成监听后才返回；不回退到随机端口，避免看似启动却连错服务。
func Start(cfg Config) (*Server, error) { return start(cfg, "0.0.0.0:18990") }
func start(cfg Config, addr string) (*Server, error) {
	if cfg.Invoke == nil || !filepath.IsAbs(cfg.Documents) {
		return nil, fmt.Errorf("调试服务缺少调用器或 Documents 绝对路径")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("调试端口监听失败: %w", err)
	}
	s := &Server{cfg: cfg, token: "1234567", started: time.Now(), gate: make(chan struct{}, 1), results: map[string]json.RawMessage{}}
	if s.cfg.Video == nil {
		s.cfg.Video = NewVideo(nil)
	}
	s.http = &http.Server{Addr: listener.Addr().String(), Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 125 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() { _ = s.http.Serve(listener) }()
	return s, nil
}

// Token 仅由宿主启动界面输出一次，不通过发现广播或未鉴权接口返回。
func (s *Server) Token() string { return s.token }
func (s *Server) Close(ctx context.Context) error {
	videoErr := s.cfg.Video.Close(ctx)
	err := s.http.Shutdown(ctx)
	if videoErr != nil {
		return videoErr
	}
	return err
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// 浏览器 WebSocket 无法设置 Authorization；此入口在升级后的首条消息鉴权。
	// 其他 HTTP 入口继续使用原有鉴权和跨源限制。
	if r.URL.Path == "/video/ws" {
		s.videoSocket(w, r)
		return
	}
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
	_, videoPath := videoRoutes[r.URL.Path]
	if r.Method != "GET" && !(r.Method == "POST" && (r.URL.Path == "/invoke" || r.URL.Path == "/script/test" || videoPath)) {
		w.Header().Set("Allow", "GET, POST")
		failure(w, 405, "请求方法不支持")
		return
	}
	switch r.URL.Path {
	case "/", "/api":
		writeJSON(w, 200, map[string]any{"ok": true, "data": map[string]any{"version": 2, "routes": routes, "videoRoutes": videoRoutes, "extra": []string{"GET /health", "GET /log", "GET /results", "POST /invoke", "POST /script/test", "GET /video/stream (Annex B H.264，限时独占采集)", "GET /video/ws (WebSocket，首消息鉴权，AGV1 二进制帧)"}, "allowedMethods": allowed, "note": "GET /selftest 会触发自检，可能点击界面或改变剪贴板、悬浮层；只用于受控测试"}})
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
	if r.URL.Path == "/video/stream" {
		if r.Method != "GET" {
			failure(w, 405, "视频下载请使用 GET")
			return
		}
		s.cfg.Video.stream(w, r)
		return
	}
	method, ok := routes[r.URL.Path]
	args := map[string]any{}
	if videoPath {
		method = videoRoutes[r.URL.Path]
		ok = true
		read := method == "video.status" || method == "video.read"
		if (read && r.Method != "GET") || (!read && r.Method != "POST") {
			failure(w, 405, "视频读取用 GET，控制用 POST")
			return
		}
		if !read {
			r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
			dec := json.NewDecoder(r.Body)
			if err := dec.Decode(&args); err != nil && err != io.EOF {
				failure(w, 400, "无效视频请求 JSON")
				return
			}
			if err := dec.Decode(new(any)); err != io.EOF {
				failure(w, 400, "请求只能包含一个 JSON 对象")
				return
			}
		}
	}
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
		r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&request); err != nil {
			failure(w, 400, "无效 JSON 或请求超过 6 MiB")
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
	if strings.HasPrefix(method, "video.") {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		data, err := s.cfg.Video.call(ctx, method, args)
		if err != nil {
			writeJSON(w, 409, map[string]any{"ok": false, "error": err.Error(), "data": data})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "data": data})
		return
	}
	if r.URL.Path == "/script/test" && r.Method != "POST" {
		failure(w, 405, "内置脚本测试请使用 POST")
		return
	}
	// 不把忙请求排成无限队列；取消请求不会提前释放尚未返回的原生调用。
	if method != "script.status" && method != "background.status" && method != "sdk.health" {
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
