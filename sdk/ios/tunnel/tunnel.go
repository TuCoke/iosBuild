// Package tunnel 是设备端的 frp 客户端管理（内网穿透）。
//
// 【为什么需要它】设备在运营商 NAT 后面，服务端主动连不进来；
// frpc 反向连到我们的公网 frps，把设备上的本地端口映射出去，
// 这样服务端就能像访问本地服务一样访问设备。
//
// 【为什么不把 frp 编进 SDK】frpc 是独立可执行文件（AutoGo 那份 12.8 MB），
// 单独放着有三个好处：
//   · 编不过 iOS 的 cgo 检查时不影响主程序
//   · 可以单独替换版本（热更新）
//   · 崩了不影响宿主，宿主重启它即可
//
// 【已知前提】启动外部可执行文件需要 platform-application / run-unsigned-code
// 权限（TrollStore 或越狱环境）；普通签名下 posix_spawn 会被系统拒绝，
// 这时 Start 会明确返回错误，而不是假装启动成功。
package tunnel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"autogov1/v2/sdk/ios/files"
	"autogov1/v2/sdk/ios/internal/native"
)

// Config 是 frpc 的连接参数。
type Config struct {
	// Server 是 frps 地址（域名或 IP）。
	Server string
	// ServerPort 是 frps 端口，0 表示 7000（frp 默认）。
	ServerPort int
	// Token 是 frps 的鉴权 token（没有就留空）。
	Token string
	// ProxyName 是这条隧道的名字（frps 侧唯一），空则用设备 id 兜底。
	ProxyName string
	// LocalPort 是设备上要被映射出去的本地端口（例如我们自己的 API 端口）。
	LocalPort int
	// RemotePort 是希望在 frps 上占用的端口（0 表示由服务端分配）。
	RemotePort int
	// Type 是代理类型，默认 tcp。
	Type string
	// TLS 是否启用（frp 的 tls_enable），默认 true。
	DisableTLS bool
}

// Status 是当前隧道状态。
type Status struct {
	Running   bool   `json:"running"`
	PID       int    `json:"pid"`
	Config    string `json:"config"`
	LogPath   string `json:"logPath"`
	Binary    string `json:"binary"`
	LastError string `json:"lastError,omitempty"`
}

const (
	// 二进制在 app 包里的候选位置（放 Resources 或包根都认）。
	binaryInResources = "frpc-ios"
	binaryInRoot      = "frpc-ios"
	// 沙盒里的工作目录。
	workDir   = "Tunnel"
	configRel = "Tunnel/frpc.toml"
	logRel    = "Tunnel/frpc.log"
	pidRel    = "Tunnel/frpc.pid"
)

var mu sync.Mutex

// BundleBinary 返回 app 包里 frpc 可执行文件的路径（找不到返回空串）。
//
// 为什么不在 Go 里猜路径：iOS 的 app 包路径带安装 UUID，猜必错；
// 所以问原生要 NSBundle.bundlePath，再按两个候选位置找（放 Resources 或包根都认）。
// 找不到时不要编一个路径出来 —— posix_spawn 会返回 EACCES，比"没找到"更难查。
func BundleBinary(ctx context.Context) string {
	var out struct {
		Path string `json:"path"`
	}
	askCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := native.Call(askCtx, "app.bundle_dir", nil, &out); err != nil || out.Path == "" {
		return ""
	}
	candidates := []string{
		filepath.Join(out.Path, "Resources", binaryInResources),
		filepath.Join(out.Path, binaryInRoot),
	}
	for _, path := range candidates {
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return path
		}
	}
	return ""
}

// Render 生成 frpc.toml。
//
// 用 TOML 而不是 INI：frp v0.52 之后 INI 已废弃，
// 新版只认 toml/yaml/json，写 INI 会得到"配置解析失败"。
func Render(cfg Config) string {
	serverPort := cfg.ServerPort
	if serverPort <= 0 {
		serverPort = 7000
	}
	proxyType := cfg.Type
	if proxyType == "" {
		proxyType = "tcp"
	}
	name := cfg.ProxyName
	if name == "" {
		name = "agov-device"
	}
	var b strings.Builder
	b.WriteString("# 由 AgovHost 生成，勿手改\n")
	b.WriteString("serverAddr = " + strconv.Quote(cfg.Server) + "\n")
	b.WriteString("serverPort = " + strconv.Itoa(serverPort) + "\n")
	if cfg.Token != "" {
		b.WriteString("auth.method = \"token\"\n")
		b.WriteString("auth.token = " + strconv.Quote(cfg.Token) + "\n")
	}
	if cfg.DisableTLS {
		b.WriteString("transport.tls.enable = false\n")
	} else {
		b.WriteString("transport.tls.enable = true\n")
	}
	b.WriteString("\n[[proxies]]\n")
	b.WriteString("name = " + strconv.Quote(name) + "\n")
	b.WriteString("type = " + strconv.Quote(proxyType) + "\n")
	b.WriteString("localIP = \"127.0.0.1\"\n")
	b.WriteString("localPort = " + strconv.Itoa(cfg.LocalPort) + "\n")
	if cfg.RemotePort > 0 {
		b.WriteString("remotePort = " + strconv.Itoa(cfg.RemotePort) + "\n")
	}
	return b.String()
}

// Start 写配置、拉进程、记 pid。
//
// 返回的 Status 里 PID 为 0 且 LastError 非空，说明没起来（原因写在 LastError）。
func Start(ctx context.Context, cfg Config) (Status, error) {
	mu.Lock()
	defer mu.Unlock()

	if cfg.Server == "" {
		return Status{}, fmt.Errorf("缺少 serverAddr")
	}
	if cfg.LocalPort <= 0 {
		return Status{}, fmt.Errorf("缺少 localPort（要被映射出去的本地端口）")
	}

	binary := BundleBinary(ctx)
	if binary == "" {
		// 把"在哪找过"一起报出来，否则只看到"找不到"没法排查。
		var dir struct {
			Path string `json:"path"`
		}
		_ = native.Call(ctx, "app.bundle_dir", nil, &dir)
		return Status{LastError: "app 包里找不到 frpc-ios；bundle=" + dir.Path},
			fmt.Errorf("找不到 frpc-ios（bundle=%s）", dir.Path)
	}

	files.EnsureDir(workDir)
	configPath := files.Path(configRel)
	logPath := files.Path(logRel)
	files.Write(configPath, Render(cfg))

	var out struct {
		PID int `json:"pid"`
	}
	spawnCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err := native.Call(spawnCtx, "utils.spawn", map[string]any{
		"path":    binary,
		"args":    []string{"-c", configPath},
		"logPath": logPath,
	}, &out)
	if err != nil {
		return Status{Config: configPath, LogPath: logPath, Binary: binary,
			LastError: err.Error()}, err
	}
	if out.PID <= 0 {
		return Status{Config: configPath, LogPath: logPath, Binary: binary,
			LastError: "进程没有返回 pid"}, fmt.Errorf("启动失败")
	}
	files.Write(files.Path(pidRel), strconv.Itoa(out.PID))
	return Status{Running: true, PID: out.PID, Config: configPath, LogPath: logPath, Binary: binary}, nil
}

// Stop 停掉隧道（没有在跑就返回 ok）。
func Stop(ctx context.Context) (Status, error) {
	mu.Lock()
	defer mu.Unlock()

	pid := readPID()
	if pid <= 0 {
		return Status{Running: false}, nil
	}
	var out struct {
		OK bool `json:"ok"`
	}
	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := native.Call(stopCtx, "utils.kill", map[string]any{"pid": pid}, &out); err != nil {
		return Status{Running: true, PID: pid, LastError: err.Error()}, err
	}
	files.Remove(files.Path(pidRel))
	return Status{Running: false, PID: pid}, nil
}

// Query 查当前状态（真去问系统进程还在不在，不只看 pid 文件）。
func Query(ctx context.Context) Status {
	mu.Lock()
	defer mu.Unlock()

	pid := readPID()
	status := Status{PID: pid, Config: files.Path(configRel), LogPath: files.Path(logRel), Binary: BundleBinary(ctx)}
	if pid <= 0 {
		return status
	}
	var out struct {
		Alive bool `json:"alive"`
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := native.Call(checkCtx, "utils.alive", map[string]any{"pid": pid}, &out); err != nil {
		status.LastError = err.Error()
		return status
	}
	status.Running = out.Alive
	if !out.Alive {
		files.Remove(files.Path(pidRel))
	}
	return status
}

// Logs 读最后 n 行日志（frpc 的输出被重定向到这里）。
func Logs(n int) string {
	if n <= 0 {
		n = 50
	}
	text := files.Read(files.Path(logRel))
	if text == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ClearLogs 清空日志（换配置重启时用，避免新旧混在一起）。
func ClearLogs() {
	files.Write(files.Path(logRel), "")
}

func readPID() int {
	text := strings.TrimSpace(files.Read(files.Path(pidRel)))
	if text == "" {
		return 0
	}
	pid, err := strconv.Atoi(text)
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}
