package debugapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"autogov1/v2/sdk/ios/video"
	"github.com/gorilla/websocket"
)

func socketService(t *testing.T) (*Video, *fakeVideo, string) {
	t.Helper()
	v, f := fakeService()
	s := &Server{cfg: Config{Video: v}, token: "1234567"}
	server := httptest.NewServer(s)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = v.Close(ctx)
		server.Close()
	})
	return v, f, "ws" + strings.TrimPrefix(server.URL, "http") + "/video/ws"
}

func socketStart(t *testing.T, address, token string, duration int) (*websocket.Conn, map[string]any) {
	t.Helper()
	header := http.Header{"Origin": {"http://127.0.0.1:18790"}}
	conn, _, err := websocket.DefaultDialer.Dial(address, header)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err = conn.WriteJSON(map[string]any{"type": "start", "token": token, "options": map[string]any{"durationSeconds": duration}}); err != nil {
		t.Fatal(err)
	}
	var reply map[string]any
	if err = conn.ReadJSON(&reply); err != nil {
		t.Fatal(err)
	}
	return conn, reply
}

func TestVideoSocketAuthAndOrigin(t *testing.T) {
	v, f, address := socketService(t)
	_, reply := socketStart(t, address, "wrong", 10)
	if reply["type"] != "error" || v.status()["running"] != false || f.closes.Load() != 0 {
		t.Fatal("鉴权失败仍申请了视频源", reply)
	}
	conn, response, err := websocket.DefaultDialer.Dial(address, http.Header{"Origin": {"https://untrusted.example"}})
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil || response == nil || response.StatusCode != 403 {
		t.Fatal("未知网站未被拒绝", err)
	}
}

func TestVideoSocketPacketsControlsAndDisconnect(t *testing.T) {
	v, f, address := socketService(t)
	conn, reply := socketStart(t, address, "1234567", 10)
	if reply["type"] != "started" || reply["protocol"] != "AGV1" {
		t.Fatal(reply)
	}
	// 第一帧不是关键帧时不能从 P 帧起播；之后每条 WS 消息保留完整 AU。
	f.frames <- video.Packet{Data: []byte{0, 0, 0, 1, 0x41}, Width: 256, Height: 512}
	frame := []byte{0, 0, 0, 1, 0x67, 0x42, 0, 0x1f, 0, 0, 0, 1, 0x68, 1, 0, 0, 0, 1, 0x65, 1}
	f.frames <- video.Packet{Data: frame, Keyframe: true, Width: 256, Height: 512, PTS: 12 * time.Millisecond}
	kind, message, err := conn.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || len(message) != videoHeaderSize+len(frame) {
		t.Fatal("未收到二进制完整帧", kind, err)
	}
	if string(message[:4]) != "AGV1" || message[4] != 1 || binary.BigEndian.Uint64(message[8:16]) != 1 ||
		binary.BigEndian.Uint64(message[16:24]) != 12000 || binary.BigEndian.Uint32(message[24:28]) != 256 ||
		binary.BigEndian.Uint32(message[28:32]) != 512 || binary.BigEndian.Uint32(message[32:36]) != uint32(len(frame)) ||
		binary.BigEndian.Uint32(message[36:40]) != 1 || !bytes.Equal(message[40:], frame) {
		t.Fatal("帧头或内容错误", message)
	}
	for _, command := range []map[string]any{{"type": "keyframe"}, {"type": "bitrate", "bitrate": 300000}} {
		if err := conn.WriteJSON(command); err != nil {
			t.Fatal(err)
		}
		if err := conn.ReadJSON(&reply); err != nil || reply["ok"] != true {
			t.Fatal("控制未确认", err, reply)
		}
	}
	if f.keys.Load() != 1 || f.bitrate.Load() != 300000 {
		t.Fatal("控制未下达到本连接视频源")
	}
	_ = conn.Close()
	waitVideo(t, func() bool { return v.status()["running"] == false })
	if f.closes.Load() == 0 {
		t.Fatal("连接断开后未释放视频源")
	}
}

func TestVideoSocketDoesNotStealAndServiceClose(t *testing.T) {
	v, f, address := socketService(t)
	first, reply := socketStart(t, address, "1234567", 10)
	if reply["type"] != "started" {
		t.Fatal(reply)
	}
	_, reply = socketStart(t, address, "1234567", 10)
	if reply["type"] != "error" || f.closes.Load() != 0 {
		t.Fatal("重复连接抢停了原视频", reply)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := v.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := first.ReadMessage(); err == nil {
		t.Fatal("服务关闭后升级连接仍然存活")
	}
	waitVideo(t, func() bool { v.mu.Lock(); defer v.mu.Unlock(); return len(v.sockets) == 0 })
}

func TestVideoSocketDeadlineAndValidation(t *testing.T) {
	v, _, address := socketService(t)
	conn, reply := socketStart(t, address, "1234567", 1)
	if reply["type"] != "started" {
		t.Fatal(reply)
	}
	if err := conn.ReadJSON(&reply); err != nil || reply["type"] != "ended" || reply["error"] != "" {
		t.Fatal("正常到期未明确通知", err, reply)
	}
	waitVideo(t, func() bool { return v.status()["running"] == false })
	if _, err := videoSocketPacket(video.Packet{Data: []byte{1}, Width: 1, Height: 1, PTS: -1}, 1, 1); err == nil {
		t.Fatal("负时间戳未拒绝")
	}
	a := []byte{0, 0, 0, 1, 0x67, 0x42, 0, 0x1f, 0, 0, 0, 1, 0x68, 1, 0, 0, 0, 1, 0x65, 1}
	b := append(append([]byte{}, a[:len(a)-1]...), 2)
	if !bytes.Equal(videoParameters(a), videoParameters(b)) {
		t.Fatal("普通视频内容变化被误判为编码配置变化")
	}
}

func TestVideoSocketConfigurationVersion(t *testing.T) {
	_, f, address := socketService(t)
	conn, reply := socketStart(t, address, "1234567", 10)
	if reply["type"] != "started" {
		t.Fatal(reply)
	}
	frame := []byte{0, 0, 0, 1, 0x67, 0x42, 0, 0x1f, 0, 0, 0, 1, 0x68, 1, 0, 0, 0, 1, 0x65, 1}
	for i, expected := range []uint32{1, 1, 2} {
		data := append([]byte{}, frame...)
		if i == 2 {
			data[7] = 0x20 // 模拟重建后的 SPS 变化。
		}
		f.frames <- video.Packet{Data: data, Keyframe: true, Width: 256, Height: 512, PTS: time.Duration(i) * time.Millisecond}
		kind, message, err := conn.ReadMessage()
		if err != nil || kind != websocket.BinaryMessage || len(message) < videoHeaderSize {
			t.Fatal(err)
		}
		if binary.BigEndian.Uint32(message[36:40]) != expected {
			t.Fatal("配置版本没有随 SPS/PPS 变化更新")
		}
	}
}
