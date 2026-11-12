package app

import (
	"autogov1/v2/agent/ios/runtime"
	"autogov1/v2/protocol"
	"context"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type blockingDevice struct{ entered, release chan struct{} }

func (*blockingDevice) Info(context.Context) (runtime.Info, error) {
	i := runtime.Info{OS: "ios", Identifier: "test-phone"}
	i.Display.Width = 828
	i.Display.Height = 1792
	return i, nil
}
func (*blockingDevice) Healthy(context.Context) error { return nil }
func (*blockingDevice) Commands() []string            { return []string{"tap"} }
func (d *blockingDevice) Execute(ctx context.Context, c protocol.Cmd) (any, error) {
	if c.Cmd == "tap" {
		close(d.entered)
		<-d.release
		return nil, ctx.Err()
	}
	return nil, nil
}
func (*blockingDevice) Tree(context.Context) ([]map[string]any, error) { return nil, nil }
func (*blockingDevice) Screenshot(context.Context) ([]byte, error) {
	return nil, fmt.Errorf("测试无截图")
}
func (*blockingDevice) OpenVideo(context.Context, runtime.VideoOptions) (runtime.VideoSource, error) {
	return nil, fmt.Errorf("测试无视频")
}
func (*blockingDevice) Unknown(error) bool { return true }

// 用户点停止后，原生清理未返回前必须保持 stopping，并禁止启动第二个 Agent。
func TestManagerStopDoesNotPretendCleanupFinished(t *testing.T) {
	d := &blockingDevice{entered: make(chan struct{}), release: make(chan struct{})}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer ws.Close()
		var reg protocol.AgentMsg
		if ws.ReadJSON(&reg) != nil {
			return
		}
		_ = ws.WriteJSON(protocol.RegistrationAck{Type: "register_ack", DeviceID: reg.DeviceID, SessionID: reg.SessionID, ServerTimeMS: time.Now().UnixMilli()})
		_ = ws.WriteJSON(protocol.Cmd{Type: "cmd", Cmd: "tap", ID: "blocked", SessionID: reg.SessionID, Fence: 1})
		for {
			if _, _, e = ws.ReadMessage(); e != nil {
				return
			}
		}
	}))
	defer server.Close()
	m := &Manager{}
	cfg := runtime.Config{Server: strings.Replace(server.URL, "http", "ws", 1) + "/agent", DataDir: t.TempDir()}
	if e := m.Start(context.Background(), d, cfg); e != nil {
		t.Fatal(e)
	}
	select {
	case <-d.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("动作未启动")
	}
	stop, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if e := m.Stop(stop); e == nil {
		t.Fatal("驱动仍在清理却报告停止成功")
	}
	if !m.Status().Running || m.Status().State != "stopping" {
		t.Fatal("清理中状态丢失")
	}
	if e := m.Start(context.Background(), d, cfg); e == nil {
		t.Fatal("清理期间允许第二次启动")
	}
	close(d.release)
	final, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if e := m.Stop(final); e != nil {
		t.Fatal(e)
	}
	if m.Status().Running {
		t.Fatal("清理结束后仍标记运行中")
	}
}
