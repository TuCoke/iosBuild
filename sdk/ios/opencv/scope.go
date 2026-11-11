package opencv

import (
	"context"
	"image"
	"sync"

	"autogov1/v2/sdk/ios/types"
)

// Scope 管理一次脚本持有的所有 Mat，包括 Clone/Crop 等派生图像。
// 脚本正常结束、失败或取消后统一释放；显式 Close 的 Mat 会提前从集合移除。
type Scope struct {
	mu     sync.Mutex
	ctx    context.Context
	closed bool
	mats   map[*Mat]struct{}
	stop   func() bool
	done   chan struct{}
}

func NewScope(ctx context.Context) (*Scope, error) {
	if ctx == nil {
		return nil, types.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := &Scope{ctx: ctx, mats: map[*Mat]struct{}{}, done: make(chan struct{})}
	s.mu.Lock()
	s.stop = context.AfterFunc(ctx, func() { s.Close() })
	s.mu.Unlock()
	return s, nil
}
func (s *Scope) check() error {
	if s == nil {
		return types.ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx == nil {
		return types.ErrClosed
	}
	return s.ctx.Err()
}
func (s *Scope) retain(m *Mat, err error) (*Mat, error) {
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		_ = m.Close()
		return nil, types.ErrClosed
	}
	m.scope = s
	s.mats[m] = struct{}{}
	s.mu.Unlock()
	return m, nil
}
func (s *Scope) Decode(ctx context.Context, raw []byte) (*Mat, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	return s.retain(Decode(ctx, raw))
}
func (s *Scope) FromImage(ctx context.Context, img image.Image) (*Mat, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	return s.retain(FromImage(ctx, img))
}
func (s *Scope) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.done == nil {
		s.closed = true
		s.mu.Unlock()
		return
	}
	if s.closed {
		s.mu.Unlock()
		<-s.done
		return
	}
	s.closed = true
	items := make([]*Mat, 0, len(s.mats))
	for m := range s.mats {
		items = append(items, m)
	}
	s.mats = map[*Mat]struct{}{}
	if s.stop != nil {
		s.stop()
	}
	s.mu.Unlock()
	for _, m := range items {
		_ = m.Close()
	}
	close(s.done)
}
