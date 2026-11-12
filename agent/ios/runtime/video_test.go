package runtime

import (
	"autogov1/v2/protocol"
	"context"
	"testing"
	"time"
)

type testVideo struct{ closing, release chan struct{} }

func (v *testVideo) Read(ctx context.Context) (VideoPacket, error) {
	<-ctx.Done()
	return VideoPacket{}, ctx.Err()
}
func (v *testVideo) RequestKeyFrame(context.Context) error { return nil }
func (v *testVideo) SetBitrate(context.Context, int) error { return nil }
func (v *testVideo) Close() error {
	select {
	case <-v.closing:
	default:
		close(v.closing)
	}
	<-v.release
	return nil
}

type videoDevice struct {
	fakeDevice
	source *testVideo
}

func (d *videoDevice) OpenVideo(context.Context, VideoOptions) (VideoSource, error) {
	return d.source, nil
}

// 停止投屏要等编码源实际释放，不能提前清空运行标记导致新流撞上旧编码器。
func TestVideoStopWaitsForRelease(t *testing.T) {
	v := &testVideo{closing: make(chan struct{}), release: make(chan struct{})}
	s := &Session{phone: &videoDevice{source: v}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.startVideo(ctx, nil, protocol.Cmd{Mode: "ws"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.stopVideo() }()
	select {
	case <-v.closing:
	case <-time.After(time.Second):
		t.Fatal("视频未开始释放")
	}
	select {
	case <-done:
		t.Fatal("编码器尚未释放就报告已停止")
	case <-time.After(20 * time.Millisecond):
	}
	close(v.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("视频释放完成后未返回")
	}
	s.videoMu.Lock()
	pending := s.videoSource != nil || s.videoCancel != nil
	s.videoMu.Unlock()
	if pending {
		t.Fatal("停止后残留视频状态")
	}
}
