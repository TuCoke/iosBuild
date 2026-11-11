package uiacc

import (
	"context"
	"sync"
	"time"

	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/types"
)

type snapshotFetcher func(context.Context) ([]axNode, error)
type snapshotManager struct {
	mu       sync.Mutex
	collect  sync.Mutex
	active   *snapshotLoop
	fetch    snapshotFetcher
	interval time.Duration
}
type snapshotLoop struct {
	mu     sync.Mutex
	refs   int
	ctx    context.Context
	cancel context.CancelFunc
	ready  chan struct{}
	done   chan struct{}
	first  bool
	nodes  []axNode
	err    error
	at     time.Time
}

var sharedSnapshots = snapshotManager{fetch: func(ctx context.Context) ([]axNode, error) {
	var out struct {
		Nodes []axNode `json:"nodes"`
	}
	err := native.Call(ctx, "uiacc.snapshot", nil, &out)
	return out.Nodes, err
}, interval: 100 * time.Millisecond}

func (m *snapshotManager) acquire() *snapshotLoop {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		ctx, cancel := context.WithCancel(context.Background())
		m.active = &snapshotLoop{ctx: ctx, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{})}
		go m.run(m.active)
	}
	m.active.refs++
	return m.active
}
func (m *snapshotManager) release(loop *snapshotLoop) {
	if loop == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if loop.refs <= 0 {
		return
	}
	loop.refs--
	if loop.refs == 0 {
		loop.cancel()
		if m.active == loop {
			m.active = nil
		}
	}
}
func (m *snapshotManager) run(loop *snapshotLoop) {
	defer close(loop.done)
	for {
		// 最后一个引用释放后可立即返回；旧原生调用若尚未退出，新一轮采集也不能与它并发。
		m.collect.Lock()
		if loop.ctx.Err() != nil {
			m.collect.Unlock()
			return
		}
		ctx, cancel := context.WithTimeout(loop.ctx, 15*time.Second)
		nodes, err := m.fetch(ctx)
		if err == nil {
			err = ctx.Err()
		}
		cancel()
		m.collect.Unlock()
		loop.mu.Lock()
		loop.nodes = append([]axNode(nil), nodes...)
		loop.err = err
		loop.at = time.Now()
		if err != nil {
			loop.nodes = nil
		}
		if !loop.first {
			loop.first = true
			close(loop.ready)
		}
		loop.mu.Unlock()
		timer := time.NewTimer(m.interval)
		select {
		case <-loop.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (loop *snapshotLoop) snapshot(ctx context.Context) ([]axNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 首帧有界等待；原生私有 API 内部阻塞不能被 Go context 强制中断，禁止谎称可以。
	wait, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	select {
	case <-wait.Done():
		return nil, wait.Err()
	case <-loop.ctx.Done():
		return nil, types.ErrClosed
	case <-loop.ready:
	}
	if loop.ctx.Err() != nil {
		return nil, types.ErrClosed
	}
	loop.mu.Lock()
	defer loop.mu.Unlock()
	if loop.err != nil {
		return nil, loop.err
	}
	if time.Since(loop.at) > 2*time.Second {
		return nil, types.ErrTimeout
	}
	return append([]axNode(nil), loop.nodes...), nil
}
