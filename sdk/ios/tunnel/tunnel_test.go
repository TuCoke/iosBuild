package tunnel

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRender 校验生成的 frpc.toml 关键字段。
//
// 为什么要测这个：frp v0.52 之后废弃了 INI，只认 toml/yaml/json；
// 字段名写错的表现是 frpc 起来后立刻退出，日志里只有一行解析错误 ——
// 这种错在真机上很难查，所以在这里锁住形状。
func TestRender(t *testing.T) {
	cfg := Config{
		Server:     "frps.example.com",
		ServerPort: 7000,
		Token:      "s3cret",
		ProxyName:  "device-1",
		LocalPort:  18989,
		RemotePort: 6001,
	}
	out := Render(cfg)
	for _, want := range []string{
		`serverAddr = "frps.example.com"`,
		"serverPort = 7000",
		`auth.token = "s3cret"`,
		`name = "device-1"`,
		`type = "tcp"`,
		`localIP = "127.0.0.1"`,
		"localPort = 18989",
		"remotePort = 6001",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("配置里缺少 %q:\n%s", want, out)
		}
	}
}

// TestRenderDefaults 缺省值要能兜住：端口、名字、类型。
func TestRenderDefaults(t *testing.T) {
	out := Render(Config{Server: "1.2.3.4", LocalPort: 8080})
	for _, want := range []string{"serverPort = 7000", `name = "agov-device"`, `type = "tcp"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("缺省值没兜住 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "remotePort") {
		t.Fatalf("RemotePort 为 0 时不该写这一行:\n%s", out)
	}
}

// TestStartValidation 参数不全会立刻报错，不会去拉进程。
func TestStartValidation(t *testing.T) {
	if _, err := Start(nil, Config{}); err == nil { //nolint:staticcheck // 明确测 nil ctx 的容错
		t.Fatal("缺少 serverAddr 应当报错")
	}
	if _, err := Start(nil, Config{Server: "1.2.3.4"}); err == nil { //nolint:staticcheck
		t.Fatal("缺少 localPort 应当报错")
	}
}

// 长日志读取必须有界，不能因 UI 每两秒刷新而重复载入整个历史文件。
func TestLogTailIsBoundedAndKeepsLatestFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frpc.log")
	text := strings.Repeat("历史连接日志\n", 12000) + "login to server failed: connection refused\n"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	tail, err := readLogTail(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) > 64*1024 || !strings.HasSuffix(tail, "login to server failed: connection refused\n") {
		t.Fatalf("未正确保留有界日志末尾，长度=%d", len(tail))
	}
}
func TestLogTailReportsMissingFile(t *testing.T) {
	if _, err := readLogTail(filepath.Join(t.TempDir(), "missing.log")); !os.IsNotExist(err) {
		t.Fatalf("文件错误被隐藏：%v", err)
	}
}
func TestRenderWritesConsoleLogs(t *testing.T) {
	text := Render(Config{Server: "example.com", LocalPort: 18989})
	for _, line := range []string{`log.to = "console"`, `log.level = "info"`, `log.disablePrintColor = true`} {
		if !strings.Contains(text, line) {
			t.Fatalf("缺少日志配置 %s", line)
		}
	}
}

// 用完整 TOML 解析而非子串匹配，防止日志配置的引号或换行被重复转义。
func TestRenderedConfigurationParsesAsTOML(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
	}
	if err != nil {
		t.Skip("需要 Python 3.11+ 的 tomllib")
	}
	cfg := Config{Server: "example.com", ServerPort: 7000, Token: "unit-token-only", ProxyName: "parse-check", LocalPort: 18990, RemotePort: 18990}
	payload, _ := json.Marshal(map[string]any{"text": Render(cfg), "token": cfg.Token})
	script := `import sys,json
try:
 import tomllib
except ImportError:
 sys.exit(77)
p=json.load(sys.stdin)
d=tomllib.loads(p['text'])
assert d['log']=={'to':'console','level':'info','disablePrintColor':True}
assert d['auth']['token']==p['token']
assert d['transport']['tls']['enable'] is True
assert d['proxies'][0]['localPort']==18990
assert d['proxies'][0]['remotePort']==18990
assert d['proxies'][0]['localIP']=='127.0.0.1'
`
	cmd := exec.Command(python, "-c", script)
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.CombinedOutput()
	if e, ok := err.(*exec.ExitError); ok && e.ExitCode() == 77 {
		t.Skip("当前 Python 不支持 tomllib")
	}
	if err != nil {
		t.Fatalf("完整 TOML 解析或结构校验失败：%v %s", err, output)
	}
}
func TestStartupFailureIncludesPersistentDiagnostic(t *testing.T) {
	status, err := Start(nil, Config{})
	if err == nil || status.StartError != err.Error() {
		t.Fatalf("启动失败未返回结构化原因：%+v %v", status, err)
	}
}
