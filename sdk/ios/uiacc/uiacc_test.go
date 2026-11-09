package uiacc

import (
	"context"
	"errors"
	"testing"
	"time"

	"autogov1/v2/sdk/ios/types"
)

func TestFindHandlesCycleAndKeepsUnknownState(t *testing.T) {
	root := &Node{ID: "root"}
	target := &Node{Text: "搜索"}
	root.Children = []*Node{target}
	target.Children = []*Node{root}
	nodes, err := Find(Snapshot{Roots: []*Node{root}}, Query{Text: "搜索"})
	if err != nil || len(nodes) != 1 || nodes[0] != target {
		t.Fatalf("循环控件树查询错误: %v, %v", nodes, err)
	}
	if nodes[0].Enabled != nil {
		t.Fatal("未知状态被误当成已知状态")
	}
}

func TestFindRejectsOversizedProviderSnapshot(t *testing.T) {
	_, err := Find(Snapshot{Roots: make([]*Node, 20001)}, Query{})
	if !errors.Is(err, types.ErrInvalid) {
		t.Fatalf("未限制采集器快照大小: %v", err)
	}
}

type delayedProvider struct{ calls int }

func (p *delayedProvider) Snapshot(context.Context) (Snapshot, error) {
	p.calls++
	s := Snapshot{}
	if p.calls >= 2 {
		s.Roots = []*Node{{ID: "ready"}}
	}
	return s, nil
}

func TestWaitObservesNewSnapshot(t *testing.T) {
	p := &delayedProvider{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	n, err := Wait(ctx, p, Query{ID: "ready"}, 20*time.Millisecond)
	if err != nil || n == nil || n.ID != "ready" || p.calls != 2 {
		t.Fatalf("未重新获取控件快照: node=%v calls=%d err=%v", n, p.calls, err)
	}
}
