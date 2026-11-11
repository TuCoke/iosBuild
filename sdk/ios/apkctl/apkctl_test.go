package apkctl

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func testSession(t *testing.T) (*Session, net.Conn) {
	t.Helper()
	left, right := net.Pipe()
	s := NewSession(context.Background())
	s.mu.Lock()
	s.conn = left
	s.mu.Unlock()
	go s.read(left)
	t.Cleanup(func() { s.Close(); right.Close() })
	return s, right
}
func writeTestFrame(w io.Writer, f frame) {
	var body bytes.Buffer
	_ = binary.Write(&body, binary.BigEndian, f.id)
	_ = binary.Write(&body, binary.BigEndian, uint32(len(f.body)+4))
	_ = binary.Write(&body, binary.BigEndian, f.status)
	body.Write(f.body)
	_, _ = io.Copy(w, &body)
}
func stringsBody(values ...string) []byte {
	var b bytes.Buffer
	for _, value := range values {
		_ = writeString(&b, value)
	}
	return b.Bytes()
}
func TestRequestUsesReferenceFrameAndResponseID(t *testing.T) {
	s, host := testSession(t)
	done := make(chan error, 1)
	go func() {
		req, err := readFrame(host)
		if err != nil {
			done <- err
			return
		}
		if req.id == 0 || req.status != 1 || !bytes.Equal(req.body, stringsBody("ctx", "1+2")) {
			done <- errors.New("请求没有按样本编码")
			return
		}
		writeTestFrame(host, frame{id: req.id, status: 0, body: stringsBody("3")})
		done <- nil
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	value, err := s.eval(ctx, "ctx", "1+2")
	if err != nil || value != "3" {
		t.Fatalf("%q %v", value, err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
func TestWaitEventAcknowledgesCallbackAndReconnectDoesNotRetainHandlers(t *testing.T) {
	s, host := testSession(t)
	called := make(chan string, 1)
	s.RegEvent(EventStop, func() { called <- "stop" })
	go writeTestFrame(host, frame{id: 21, status: 6, body: stringsBody(lifecycleContext, "stop")})
	ack, err := readFrame(host)
	if err != nil || ack.id != 21 || ack.status != 0 {
		t.Fatalf("没有正确应答等待事件: %+v %v", ack, err)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("未收到生命周期事件")
	}
	s.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.messageCB != nil || len(s.events) != 0 {
		t.Fatal("关闭后回调未释放")
	}
}
func TestCloseReleasesPendingRequest(t *testing.T) {
	s, host := testSession(t)
	done := make(chan error, 1)
	go func() { _, err := s.eval(context.Background(), "ctx", "long()"); done <- err }()
	if _, err := readFrame(host); err != nil {
		t.Fatal(err)
	}
	s.Close()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("请求在关闭后仍阻塞")
	}
}
func TestRejectsTruncatedOrOversizedFrame(t *testing.T) {
	for _, length := range []uint32{0, 3, maxFrame + 1} {
		var b bytes.Buffer
		_ = binary.Write(&b, binary.BigEndian, uint32(1))
		_ = binary.Write(&b, binary.BigEndian, length)
		if _, err := readFrame(&b); err == nil {
			t.Fatal("接受了无效长度", length)
		}
	}
	if _, err := readString(bytes.NewReader([]byte{0, 0, 0, 4, 1})); err == nil {
		t.Fatal("接受了截断字符串")
	}
}
