package debugapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func serverForTest(t *testing.T, call Invoke) *Server {
	t.Helper()
	if call == nil {
		call = func(context.Context, string, map[string]any) (json.RawMessage, error) {
			return json.RawMessage(`{"ok":true}`), nil
		}
	}
	s, err := start(Config{Documents: t.TempDir(), Invoke: call}, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}
func req(s *Server, method, path, body string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth {
		r.Header.Set("Authorization", "Bearer "+s.Token())
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func TestAuthentication(t *testing.T) {
	s := serverForTest(t, nil)
	for _, p := range []string{"/status", "/selftest", "/log", "/api", "/results", "/health"} {
		if w := req(s, "GET", p, "", false); w.Code != 401 {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
	if w := req(s, "GET", "/status?token="+s.Token(), "", false); w.Code != 200 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("GET", "/status?token="+s.Token(), nil)
	r.Header.Set("Origin", "https://example.org")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	other := serverForTest(t, nil)
	if s.Token() != "1234567" || other.Token() != "1234567" {
		t.Fatal("调试 token 必须保持用户指定的固定值")
	}
	if w := req(s, "GET", "/status?token=wrong", "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := req(s, "GET", "/api", "", true); strings.Contains(w.Body.String(), s.Token()) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("发现接口泄漏 token 或允许缓存")
	}
}
func TestReadRoutes(t *testing.T) {
	var called string
	s := serverForTest(t, func(_ context.Context, m string, _ map[string]any) (json.RawMessage, error) {
		called = m
		return json.RawMessage(`{"ok":true}`), nil
	})
	for p, m := range routes {
		w := req(s, "GET", p, "", true)
		if w.Code != 200 || called != m {
			t.Fatalf("%s %d %s", p, w.Code, called)
		}
	}
	if w := req(s, "POST", "/status", "", true); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := req(s, "GET", "/missing", "", true); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
func TestWhitelistAndValidation(t *testing.T) {
	s := serverForTest(t, nil)
	for _, m := range []string{"utils.exec", "utils.spawn", "sdk.install", "app.uninstall", "device.reboot", "script.remote", "script.executeFile", "script.run"} {
		if w := req(s, "POST", "/invoke", `{"method":"`+m+`"}`, true); w.Code != 403 {
			t.Fatalf("%s %d", m, w.Code)
		}
	}
	for _, b := range []string{`{"method":"motion.tap","extra":1}`, `{"method":"motion.tap"} {}`, strings.Repeat("x", 65537)} {
		if w := req(s, "POST", "/invoke", b, true); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if w := req(s, "POST", "/invoke", `{"method":"motion.tap","args":{"x":0.5,"y":0.5}}`, true); w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestBuiltinScriptRejectsUntrustedSource(t *testing.T) {
	s := serverForTest(t, func(_ context.Context, m string, args map[string]any) (json.RawMessage, error) {
		if m != "script.run" || len(args) != 0 {
			t.Error("内置测试接收了外部源码")
		}
		return json.RawMessage(`{"ok":true}`), nil
	})
	if w := req(s, "POST", "/script/test", `{"source":"untrusted"}`, true); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := req(s, "POST", "/script/test", "", true); w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestCanceledCallRetainsGateUntilNativeReturns(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	s := serverForTest(t, func(_ context.Context, method string, _ map[string]any) (json.RawMessage, error) {
		if method == "script.status" {
			return json.RawMessage(`{"ok":true,"data":{"running":true}}`), nil
		}
		close(entered)
		<-release
		return json.RawMessage(`{"ok":true}`), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("GET", "/selftest", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+s.Token())
	done := make(chan struct{})
	go func() { s.ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	<-entered
	cancel()
	if w := req(s, "GET", "/selftest", "", true); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := req(s, "GET", "/health", "", true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := req(s, "GET", "/worker", "", true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	close(release)
	<-done
	if len(s.gate) != 0 {
		t.Fatal("闸门未回收")
	}
}
func TestLogAndResults(t *testing.T) {
	s := serverForTest(t, nil)
	if w := req(s, "GET", "/log", "", true); w.Code != 404 {
		t.Fatal(w.Code)
	}
	content := "日志一\n日志二\n"
	if err := os.WriteFile(filepath.Join(s.cfg.Documents, "app.log"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if w := req(s, "GET", "/log?path=/etc/passwd", "", true); w.Code != 200 || w.Body.String() != content {
		t.Fatal(w.Code, w.Body.String())
	}
	req(s, "GET", "/selftest", "", true)
	if w := req(s, "GET", "/results", "", true); !strings.Contains(w.Body.String(), "/selftest") {
		t.Fatal(w.Body.String())
	}
}
func TestInvalidBackend(t *testing.T) {
	s := serverForTest(t, func(context.Context, string, map[string]any) (json.RawMessage, error) {
		return json.RawMessage(`bad`), nil
	})
	if w := req(s, "GET", "/status", "", true); w.Code != 502 {
		t.Fatal(w.Code)
	}
}
func TestRejectLogSymlink(t *testing.T) {
	s := serverForTest(t, nil)
	p := filepath.Join(t.TempDir(), "secret")
	_ = os.WriteFile(p, []byte("secret"), 0600)
	if err := os.Symlink(p, filepath.Join(s.cfg.Documents, "app.log")); err != nil {
		t.Skip("当前环境不能创建软链接")
	}
	if w := req(s, "GET", "/log", "", true); w.Code != 403 {
		t.Fatal(w.Code)
	}
}
