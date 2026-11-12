package runtime

import (
	"autogov1/v2/agent/transport"
	"autogov1/v2/protocol"
	"context"
	"fmt"
	"log"
	"time"
)

// 原生视频直接读取 SDK 编码器，不绕调试 HTTP/FRP，也没有 120 秒调试采集上限。
func (s *Session) startVideo(ctx context.Context, conn *transport.Conn, c protocol.Cmd) error {
	s.videoMu.Lock()
	running := s.videoCancel != nil
	pending := s.videoSource != nil && !running
	s.videoMu.Unlock()
	if running {
		return nil
	}
	if pending {
		return fmt.Errorf("上次 iOS 视频尚未释放，请先停止重试")
	}
	if c.Mode != "" && c.Mode != "ws" {
		return fmt.Errorf("iOS Agent 当前只支持 WS 投屏")
	}
	options := VideoOptions{Width: 640, FPS: 15, Bitrate: 1000000}
	if c.VWidth > 0 {
		options.Width = c.VWidth
	}
	if c.VFps > 0 {
		options.FPS = c.VFps
	}
	if c.Bitrate > 0 {
		options.Bitrate = c.Bitrate
	}
	run, cancel := context.WithCancel(ctx)
	source, err := s.phone.OpenVideo(run, options)
	if err != nil {
		cancel()
		return err
	}
	done := make(chan struct{})
	s.videoMu.Lock()
	s.videoCancel = cancel
	s.videoDone = done
	s.videoSource = source
	s.videoMu.Unlock()

	go func() {
		defer func() {
			cancel()
			err := source.Close()
			s.videoMu.Lock()
			s.videoError = err
			if err == nil {
				s.videoSource = nil
			}
			s.videoCancel = nil
			close(done)
			s.videoMu.Unlock()
		}()

		for {
			p, e := source.Read(run)
			if e != nil {
				if run.Err() == nil {
					log.Printf("iOS 视频读取结束: %v", e)
				}
				return
			}
			if len(p.Data) == 0 {
				continue
			}
			if e = conn.WriteBinary(transport.BinVideo, p.Data); e != nil {
				return
			}
		}
	}()
	return nil
}
func (s *Session) stopVideo() error {
	s.videoMu.Lock()
	cancel, done, source := s.videoCancel, s.videoDone, s.videoSource
	s.videoMu.Unlock()
	if source == nil {
		return nil
	}
	if cancel != nil {
		cancel()
		<-done
	}
	s.videoMu.Lock()
	source = s.videoSource
	s.videoMu.Unlock()
	if source != nil {
		if err := source.Close(); err != nil {
			return err
		}
		s.videoMu.Lock()
		s.videoSource = nil
		s.videoError = nil
		s.videoMu.Unlock()
	}
	return nil
}

func (s *Session) videoCommand(kind string, bitrate int) error {
	s.videoMu.Lock()
	source := s.videoSource
	s.videoMu.Unlock()
	if source == nil {
		return fmt.Errorf("iOS 视频未运行")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if kind == "keyframe" {
		return source.RequestKeyFrame(ctx)
	}
	return source.SetBitrate(ctx, bitrate)
}
