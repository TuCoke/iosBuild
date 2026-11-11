package uiacc

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"autogov1/v2/sdk/ios/types"
)

func TestCacheSharedLifecycle(t *testing.T) {
	var calls atomic.Int32
	manager := snapshotManager{interval: time.Hour, fetch: func(context.Context) ([]axNode, error) { calls.Add(1); return []axNode{{Text: "hello"}}, nil }}
	a, b := manager.acquire(), manager.acquire()
	if a != b {
		t.Fatal("没有共享采集器")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	nodes, err := a.snapshot(ctx)
	if err != nil || len(nodes) != 1 {
		t.Fatal(nodes, err)
	}
	if calls.Load() != 1 {
		t.Fatal("同一帧重复采集")
	}
	manager.release(a)
	if _, err = b.snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	manager.release(b)
	select {
	case <-b.done:
	case <-ctx.Done():
		t.Fatal("释放后没有停止采集")
	}
	if _, err = b.snapshot(ctx); !errors.Is(err, types.ErrClosed) {
		t.Fatal(err)
	}
}
func TestCacheFailureClearsStaleNodes(t *testing.T) {
	var calls atomic.Int32
	failure := errors.New("采集失败")
	manager := snapshotManager{interval: time.Millisecond, fetch: func(context.Context) ([]axNode, error) {
		if calls.Add(1) == 1 {
			return []axNode{{Text: "old"}}, nil
		}
		return nil, failure
	}}
	loop := manager.acquire()
	defer manager.release(loop)
	deadline := time.Now().Add(time.Second)
	for {
		loop.mu.Lock()
		err := loop.err
		loop.mu.Unlock()
		if errors.Is(err, failure) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("未采集到错误")
		}
		time.Sleep(time.Millisecond)
	}
	nodes, err := loop.snapshot(context.Background())
	if !errors.Is(err, failure) || nodes != nil {
		t.Fatal("采集失败仍使用旧控件", nodes, err)
	}
}
func TestUnknownStateDoesNotMatchFalse(t *testing.T) {
	var node axNode
	if err := json.Unmarshal([]byte(`{"text":"hello"}`), &node); err != nil {
		t.Fatal(err)
	}
	want := false
	u := &Uiacc{enabled: &want}
	if u.match(&node) {
		t.Fatal("未知状态被当成 false")
	}
	node.Enabled = &want
	if !u.match(&node) {
		t.Fatal("明确 false 未匹配")
	}
}
func TestWaitStopsOnRelease(t *testing.T) {
	manager := snapshotManager{interval: time.Hour, fetch: func(context.Context) ([]axNode, error) { return nil, nil }}
	loop := manager.acquire()
	u := &Uiacc{lease: loop}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := u.WaitForContext(ctx); done <- err }()
	<-loop.ready
	u.mu.Lock()
	u.released = true
	u.mu.Unlock()
	manager.release(loop)
	select {
	case err := <-done:
		if !errors.Is(err, types.ErrClosed) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("释放后等待没有退出")
	}
}

func TestTaskContextReleasesOnlyOwnReference(t *testing.T) {
	a := New()
	defer a.Release()
	ctx, cancel := context.WithCancel(context.Background())
	b := NewWithContext(ctx)
	if a.lease != b.lease {
		t.Fatal("引用没有共享采集器")
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		released := b.released
		b.mu.Unlock()
		if released {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("任务取消未释放选择器")
		}
		time.Sleep(time.Millisecond)
	}
	a.mu.Lock()
	released := a.released
	a.mu.Unlock()
	if released {
		t.Fatal("一个任务取消释放了其他任务的引用")
	}
}
