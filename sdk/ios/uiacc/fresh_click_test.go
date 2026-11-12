package uiacc

import (
	"context"
	"testing"
	"time"
)

func TestFreshClickUsesUniqueCurrentGeometry(t *testing.T) {
	old := axNode{Identifier: "submit", Text: "保存", Bundle: "test.app", Role: "button", Right: 100, Bottom: 50}
	current := old
	current.Left = 100
	current.Right = 200
	got, err := freshClickNode(old, time.Now().Add(-time.Hour), []axNode{current})
	if err != nil || got.Left != 100 {
		t.Fatal("应重新定位唯一节点而非使用旧坐标", got, err)
	}
	for _, nodes := range [][]axNode{nil, {current, current}} {
		if _, err := freshClickNode(old, time.Now(), nodes); err == nil {
			t.Fatal("消失或重复节点未拒绝")
		}
	}
	for _, change := range []func(*axNode){func(n *axNode) { n.Bundle = "other.app" }, func(n *axNode) { n.Text = "删除" }, func(n *axNode) { b := false; n.Enabled = &b }} {
		changed := current
		change(&changed)
		if _, err := freshClickNode(old, time.Now(), []axNode{changed}); err == nil {
			t.Fatal("页面/语义改变后仍允许点击")
		}
	}
}

func TestUnidentifiedNodeExpiresAndCanceledObjectStops(t *testing.T) {
	node := axNode{Left: 1, Top: 2, Right: 50, Bottom: 60}
	if _, err := freshClickNode(node, time.Now().Add(-time.Second), []axNode{node}); err == nil {
		t.Fatal("无标识旧节点没有过期")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := validateClick(context.Background(), &UiObject{node: node, life: ctx}); err == nil {
		t.Fatal("任务结束后仍可操作旧节点")
	}
	if _, err := validateClick(nil, &UiObject{}); err == nil {
		t.Fatal("nil context 未拒绝")
	}
}
