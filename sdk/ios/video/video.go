// Package video 提供有界缓冲的 Annex B H.264 输出，不包含 WebRTC 或 WebSocket。
package video

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/types"
)

type Config struct {
	AutoRotation bool `json:"autoRotation"`
	MaxSize      int  `json:"maxSize"`
	Bitrate      int  `json:"bitrate"`
	FPS          int  `json:"fps"`
	Rotation     int  `json:"rotation"`
	QueueBytes   int  `json:"queueBytes"`
}

// Packet 每包对应一个访问单元；IDR 包前附 SPS/PPS，PTS 从本次流启动起按微秒递增。
type Packet struct {
	Data          []byte
	PTS           time.Duration
	Keyframe      bool
	Width, Height int
}
type Stream struct {
	token    uint64
	mu       sync.Mutex
	closed   bool
	stop     func() bool
	readGate chan struct{}
}

var sequence atomic.Uint64

func Start(ctx context.Context, c Config) (*Stream, error) {
	if ctx == nil {
		return nil, types.ErrInvalid
	}
	if c.MaxSize == 0 {
		c.MaxSize = 960
	}
	if c.FPS == 0 {
		c.FPS = 15
	}
	if c.Bitrate == 0 {
		c.Bitrate = 1500000
	}
	if c.QueueBytes == 0 {
		c.QueueBytes = 4 << 20
	}
	if c.MaxSize < 64 || c.MaxSize > 4096 || c.FPS < 1 || c.FPS > 60 || c.Bitrate < 64000 || c.Bitrate > 50000000 || c.Rotation < 0 || c.Rotation > 3 || c.QueueBytes < 256<<10 || c.QueueBytes > 32<<20 {
		return nil, fmt.Errorf("%w: 视频配置越界", types.ErrInvalid)
	}
	id := sequence.Add(1)
	s := &Stream{token: id, readGate: make(chan struct{}, 1)}
	if err := native.Call(ctx, "video.start", map[string]any{"token": id, "config": c}, nil); err != nil {
		// 原生启动可能已发生时，按自己的 token 清理，不停止其他调用者的流。
		if errors.Is(err, types.ErrUnknown) {
			_ = s.Close()
		}
		return nil, err
	}
	s.mu.Lock()
	s.stop = context.AfterFunc(ctx, func() { _ = s.Close() })
	s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// Read 按需读取，不启动回调消费者协程；调用方变慢时原生缓冲有上限。
func (s *Stream) Read(ctx context.Context) (Packet, error) {
	if ctx == nil {
		return Packet{}, types.ErrInvalid
	}
	select {
	case s.readGate <- struct{}{}:
		defer func() { <-s.readGate }()
	case <-ctx.Done():
		return Packet{}, ctx.Err()
	}
	for {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return Packet{}, io.EOF
		}
		r, err := native.Raw(ctx, "video.read", map[string]any{"token": s.token})
		if errors.Is(err, types.ErrClosed) {
			_ = s.Close()
			return Packet{}, io.EOF
		}
		if err != nil {
			if ctx.Err() == nil {
				_ = s.Close()
			}
			return Packet{}, err
		}
		if len(r.Data) > 0 {
			return Packet{Data: r.Data, PTS: time.Duration(r.PTS) * time.Microsecond, Keyframe: r.Keyframe, Width: r.Width, Height: r.Height}, nil
		}
		// 原生读取最多等待 100ms，随后检查 context，不会无限阻塞取消。
	}
}
func (s *Stream) RequestKeyFrame(ctx context.Context) error {
	return s.control(ctx, "video.keyframe", nil)
}
func (s *Stream) SetBitrate(ctx context.Context, bps int) error {
	if bps < 64000 || bps > 50000000 {
		return types.ErrInvalid
	}
	return s.control(ctx, "video.bitrate", map[string]any{"bitrate": bps})
}
func (s *Stream) control(ctx context.Context, op string, args map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return types.ErrClosed
	}
	if args == nil {
		args = map[string]any{}
	}
	args["token"] = s.token
	return native.Call(ctx, op, args, nil)
}
func (s *Stream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := native.Call(ctx, "video.stop", map[string]any{"token": s.token}, nil)
	if err != nil && !errors.Is(err, types.ErrClosed) {
		return err
	}
	s.closed = true
	if s.stop != nil {
		s.stop()
	}
	return nil
}
