package debugapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"autogov1/v2/sdk/ios/video"
)

type fakeVideo struct {
	frames       chan video.Packet
	closes       atomic.Int32
	keys         atomic.Int32
	bitrate      atomic.Int32
	closeFailure atomic.Bool
}

func (f *fakeVideo) Read(ctx context.Context) (video.Packet, error) {
	select {
	case packet := <-f.frames:
		return packet, nil
	case <-ctx.Done():
		return video.Packet{}, ctx.Err()
	}
}
func (f *fakeVideo) Close() error {
	f.closes.Add(1)
	if f.closeFailure.Load() {
		return errors.New("close failed")
	}
	return nil
}
func (f *fakeVideo) RequestKeyFrame(context.Context) error     { f.keys.Add(1); return nil }
func (f *fakeVideo) SetBitrate(_ context.Context, n int) error { f.bitrate.Store(int32(n)); return nil }
func fakeService() (*Video, *fakeVideo) {
	f := &fakeVideo{frames: make(chan video.Packet, 10)}
	return NewVideo(func(context.Context, video.Config) (VideoSource, error) { return f, nil }), f
}
func waitVideo(t *testing.T, condition func() bool) {
	t.Helper()
	end := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(end) {
			t.Fatal("视频状态未按时变化")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestVideoLifecycleAndControls(t *testing.T) {
	v, f := fakeService()
	defer v.Close(context.Background())
	if _, err := v.call(context.Background(), "video.start", map[string]any{"durationSeconds": 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := v.start(nil, false); err == nil {
		t.Fatal("重复启动未拒绝")
	}
	frame := []byte{0, 0, 0, 1, 0x65, 1}
	f.frames <- video.Packet{Data: frame, Keyframe: true, Width: 256, Height: 512, PTS: 10 * time.Millisecond}
	waitVideo(t, func() bool { return v.status()["frames"].(uint64) == 1 })
	data, err := v.call(context.Background(), "video.read", nil)
	if err != nil || !bytes.Equal(data.(map[string]any)["data"].([]byte), frame) {
		t.Fatalf("读取不正确: %v", err)
	}
	if _, err = v.call(context.Background(), "video.keyframe", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = v.call(context.Background(), "video.bitrate", map[string]any{"bitrate": float64(300000)}); err != nil {
		t.Fatal(err)
	}
	if f.keys.Load() != 1 || f.bitrate.Load() != 300000 {
		t.Fatal("控制未下达到视频源")
	}
	if _, err = v.call(context.Background(), "video.stop", nil); err != nil {
		t.Fatal(err)
	}
	if v.status()["running"] != false || f.closes.Load() == 0 {
		t.Fatal("未清理视频资源")
	}
}

func TestVideoAutoTimeoutAndCleanupFailure(t *testing.T) {
	v, f := fakeService()
	f.closeFailure.Store(true)
	r, err := v.start(map[string]any{"durationSeconds": 1}, false)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.done:
	case <-time.After(3 * time.Second):
		t.Fatal("超时未自动退出")
	}
	if _, err = v.start(nil, false); err == nil {
		t.Fatal("清理失败时不能启动第二条流")
	}
	f.closeFailure.Store(false)
	if err = v.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = v.start(nil, false); err == nil {
		t.Fatal("服务已关闭还允许采集")
	}
}

func TestVideoRejectsInvalidAndUnknownArguments(t *testing.T) {
	v, _ := fakeService()
	defer v.Close(context.Background())
	for _, args := range []map[string]any{{"durationSeconds": 121}, {"durationSeconds": -1}, {"durationSeconds": 1.5}, {"token": 123}, {"queueBytes": 8 << 20}} {
		if _, err := v.start(args, false); err == nil {
			t.Fatalf("非法参数被接受: %v", args)
		}
	}
}

func TestVideoHTTPAuthenticationAndRouting(t *testing.T) {
	v, f := fakeService()
	_ = f
	s, err := start(Config{Documents: t.TempDir(), Video: v, Invoke: func(context.Context, string, map[string]any) (json.RawMessage, error) {
		t.Error("视频不能走普通 JSON 原生入口")
		return nil, nil
	}}, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	for _, path := range []string{"/video/stream", "/video/status", "/video/packet"} {
		if w := req(s, "GET", path, "", false); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	if w := req(s, "GET", "/video/start", "", true); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := req(s, "POST", "/video/start", `{"durationSeconds":5}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := req(s, "POST", "/invoke", `{"method":"video.status"}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := req(s, "POST", "/video/stop", "{}", true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := req(s, "POST", "/video/start", "{} {}", true); w.Code != 400 {
		t.Fatal(w.Code)
	}
}

func TestVideoStreamCancelReleasesSource(t *testing.T) {
	v, f := fakeService()
	defer v.Close(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("GET", "/video/stream?durationSeconds=10", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); v.stream(w, r) }()
	waitVideo(t, func() bool { return v.status()["running"] == true })
	f.frames <- video.Packet{Data: []byte{0, 0, 0, 1, 0x41}, Keyframe: false}
	f.frames <- video.Packet{Data: []byte{0, 0, 0, 1, 0x65}, Keyframe: true}
	waitVideo(t, func() bool { return v.status()["frames"].(uint64) >= 2 })
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("断开后下载未停止")
	}
	if f.closes.Load() == 0 {
		t.Fatal("未释放编码器")
	}
	// 取消可能先于关键帧写出；若有输出，必须从关键帧开始。
	if w.Body.Len() > 0 && !bytes.HasPrefix(w.Body.Bytes(), []byte{0, 0, 0, 1, 0x65}) {
		t.Fatal("文件从 P 帧开始")
	}
}

func TestVideoStreamWritesRealBinaryStartingWithIDR(t *testing.T) {
	v, f := fakeService()
	defer v.Close(context.Background())
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		v.stream(w, httptest.NewRequest("GET", "/video/stream?durationSeconds=1", nil))
	}()
	waitVideo(t, func() bool { return v.status()["running"] == true })
	f.frames <- video.Packet{Data: []byte{0, 0, 0, 1, 0x41}, Keyframe: false}
	key := []byte{0, 0, 0, 1, 0x67, 0, 0, 0, 1, 0x68, 0, 0, 0, 1, 0x65}
	f.frames <- video.Packet{Data: key, Keyframe: true}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("限时下载未结束")
	}
	if w.Code != 200 || w.Header().Get("Content-Type") != "video/h264" || !bytes.Equal(w.Body.Bytes(), key) {
		t.Fatalf("二进制流异常: %d %q %x", w.Code, w.Header().Get("Content-Type"), w.Body.Bytes())
	}
	if f.closes.Load() == 0 {
		t.Fatal("下载结束没有释放采集器")
	}
}
