package runtime

import (
	"autogov1/v2/protocol"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeDevice struct {
	calls   atomic.Int32
	entered chan struct{}
	exited  chan struct{}
	block   bool
}

func (f *fakeDevice) Info(context.Context) (Info, error) {
	v := Info{OS: "ios", OSVersion: "16.5", Identifier: "owned-test-phone", Name: "测试手机"}
	v.Display.Width = 828
	v.Display.Height = 1792
	return v, nil
}
func (*fakeDevice) Healthy(context.Context) error { return nil }
func (*fakeDevice) Commands() []string {
	return []string{"tap", "current_app", "input_reset", "screen_state"}
}
func (f *fakeDevice) Execute(ctx context.Context, c protocol.Cmd) (any, error) {
	if c.Cmd == "current_app" {
		return json.RawMessage(`"com.apple.Preferences"`), nil
	}
	if c.Cmd == "tap" {
		f.calls.Add(1)
		if f.block {
			close(f.entered)
			<-ctx.Done()
			close(f.exited)
			return nil, ctx.Err()
		}
	}
	return map[string]any{"submitted": true}, nil
}
func (*fakeDevice) Tree(context.Context) ([]map[string]any, error) { return nil, nil }
func (*fakeDevice) Screenshot(context.Context) ([]byte, error) {
	return []byte{255, 216, 255, 217}, nil
}
func (*fakeDevice) OpenVideo(context.Context, VideoOptions) (VideoSource, error) {
	return nil, fmt.Errorf("测试不启动视频")
}
func (*fakeDevice) Unknown(e error) bool { return errors.Is(e, context.Canceled) }
func readReply(w *websocket.Conn, id string) (protocol.AgentMsg, error) {
	for {
		var m protocol.AgentMsg
		if e := w.ReadJSON(&m); e != nil {
			return m, e
		}
		if m.ID == id {
			return m, nil
		}
	}
}
func runFixture(t *testing.T, d *fakeDevice, dir string, script func(*websocket.Conn, protocol.AgentMsg) error) {
	t.Helper()
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-only-token" || r.URL.RawQuery != "" {
			done <- fmt.Errorf("鉴权没有独立放入请求头")
			http.Error(w, "denied", 403)
			return
		}
		ws, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			done <- e
			return
		}
		defer ws.Close()
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		var reg protocol.AgentMsg
		if e = ws.ReadJSON(&reg); e != nil {
			done <- e
			return
		}
		if reg.Platform != "ios" || reg.SessionID == "" || len(reg.Commands) == 0 {
			done <- fmt.Errorf("平台注册不完整")
			return
		}
		if e = ws.WriteJSON(protocol.RegistrationAck{Type: "register_ack", DeviceID: reg.DeviceID, SessionID: reg.SessionID}); e != nil {
			done <- e
			return
		}
		done <- script(ws, reg)
	}))
	defer server.Close()
	session, e := NewSession(d, Config{Server: strings.Replace(server.URL, "http", "ws", 1) + "/agent", Token: "test-only-token", DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	_ = session.Run(ctx)
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("协议测试没有完成")
	}
}

// 同一操作的重复投递及重连后重放都必须复用落盘回执，不能再次点击。
func TestPersistentReceiptAndPlatformIsolation(t *testing.T) {
	d := &fakeDevice{}
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		runFixture(t, d, dir, func(w *websocket.Conn, reg protocol.AgentMsg) error {
			cmd := protocol.Cmd{Type: "cmd", Cmd: "tap", ID: "stable-request", SessionID: reg.SessionID, Fence: 1, X: .5, Y: .5}
			// 连接代次变化后重复请求使用同一业务参数；执行日志不把传输代次当作新的动作。
			for j := 0; j < 2; j++ {
				if e := w.WriteJSON(cmd); e != nil {
					return e
				}
				m, e := readReply(w, cmd.ID)
				if e != nil {
					return e
				}
				if !m.OK || m.State != "returned" {
					return fmt.Errorf("重复请求回执异常: %s %s", m.State, m.Msg)
				}
			}
			current := protocol.Cmd{Type: "cmd", Cmd: "current_app", ID: "current", SessionID: reg.SessionID}
			if e := w.WriteJSON(current); e != nil {
				return e
			}
			m, e := readReply(w, current.ID)
			if e != nil {
				return e
			}
			if m.Msg != "com.apple.Preferences" || len(m.Data) != 0 {
				return fmt.Errorf("前台应用协议丢失: %s %s", m.Msg, m.Data)
			}
			return nil
		})
	}
	if d.calls.Load() != 1 {
		t.Fatalf("重复投递实际点击了 %d 次", d.calls.Load())
	}
}

// 正在执行的动作取消后必须等驱动退出，且只报告未知，不能伪造未执行或成功。
func TestCancellationWaitsForDriver(t *testing.T) {
	d := &fakeDevice{block: true, entered: make(chan struct{}), exited: make(chan struct{})}
	runFixture(t, d, t.TempDir(), func(w *websocket.Conn, reg protocol.AgentMsg) error {
		if e := w.WriteJSON(protocol.Cmd{Type: "cmd", Cmd: "tap", ID: "active", SessionID: reg.SessionID, Fence: 1, X: .5, Y: .5}); e != nil {
			return e
		}
		select {
		case <-d.entered:
		case <-time.After(time.Second):
			return fmt.Errorf("动作未进入驱动")
		}
		if e := w.WriteJSON(protocol.Cmd{Type: "cmd", Cmd: "command_cancel", ID: "cancel", RequestID: "active", SessionID: reg.SessionID}); e != nil {
			return e
		}
		m, e := readReply(w, "active")
		if e != nil {
			return e
		}
		if m.State != "unknown" || m.OK {
			return fmt.Errorf("运行中取消被错误标记: %+v", m)
		}
		select {
		case <-d.exited:
			return nil
		default:
			return fmt.Errorf("驱动尚未退出就报告动作结束")
		}
	})
}
func TestConfigAndInstanceOwnership(t *testing.T) {
	dir := t.TempDir()
	base := Config{Server: "wss://example.com/agent", DataDir: dir, Token: "x"}
	if e := ValidateConfig(base); e != nil {
		t.Fatal(e)
	}
	for _, u := range []string{"ws://example.com/agent", "wss://user:password@example.com/agent", "wss://example.com/agent?token=secret"} {
		c := base
		c.Server = u
		if ValidateConfig(c) == nil {
			t.Fatal("错误接受不安全地址", u)
		}
	}
	release, e := lockInstance(dir)
	if e != nil {
		t.Fatal(e)
	}
	if second, e := lockInstance(dir); e == nil {
		second()
		t.Fatal("同目录允许了两个 Agent")
	}
	release()
	release, e = lockInstance(dir)
	if e != nil {
		t.Fatal(e)
	}
	release()
}
