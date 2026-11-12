package uiacc

import (
	"context"
	"fmt"
	"time"

	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/types"
)

func freshClickNode(old axNode, captured time.Time, nodes []axNode) (axNode, error) {
	var found axNode
	count := 0
	identified := old.Identifier != "" || old.Text != "" || old.Value != ""
	if !identified && (captured.IsZero() || time.Since(captured) > 500*time.Millisecond) {
		return found, fmt.Errorf("%w: 无标识节点已过期，请重新查询", types.ErrUnknown)
	}
	for _, node := range nodes {
		if node.Bundle != old.Bundle || node.Class != old.Class || node.Role != old.Role ||
			node.Identifier != old.Identifier || node.Text != old.Text || node.Value != old.Value {
			continue
		}
		if !identified && (node.Left != old.Left || node.Top != old.Top || node.Right != old.Right || node.Bottom != old.Bottom) {
			continue
		}
		if node.Enabled != nil && !*node.Enabled {
			continue
		}
		if node.Right <= node.Left || node.Bottom <= node.Top {
			continue
		}
		found, count = node, count+1
	}
	if count != 1 {
		return axNode{}, fmt.Errorf("%w: 当前页面节点匹配数为 %d，已阻止使用旧坐标", types.ErrUnknown, count)
	}
	return found, nil
}

func validateClick(ctx context.Context, object *UiObject) (axNode, error) {
	if ctx == nil || object == nil {
		return axNode{}, types.ErrInvalid
	}
	if object.life != nil && object.life.Err() != nil {
		return axNode{}, types.ErrClosed
	}
	expected := object.foreground
	if expected == "" {
		expected = object.node.Bundle
	}
	if expected == "" {
		return axNode{}, fmt.Errorf("%w: 无法确认节点所属前台应用，请重新查询", types.ErrUnknown)
	}
	currentApp, err := clickForeground(ctx)
	if err != nil {
		return axNode{}, err
	}
	if currentApp != expected {
		return axNode{}, fmt.Errorf("%w: 前台应用已变化，请重新查询节点", types.ErrUnknown)
	}
	// 点击必须采集新快照，不能再次从同一份缓存取出旧节点。
	var current struct {
		Nodes []axNode `json:"nodes"`
	}
	sharedSnapshots.collect.Lock()
	err = native.Call(ctx, "uiacc.snapshot", nil, &current)
	sharedSnapshots.collect.Unlock()
	if err != nil {
		return axNode{}, err
	}
	currentApp, err = clickForeground(ctx)
	if err != nil {
		return axNode{}, err
	}
	if currentApp != expected {
		return axNode{}, fmt.Errorf("%w: 采集过程中前台应用已变化", types.ErrUnknown)
	}
	return freshClickNode(object.node, object.captured, current.Nodes)
}

func clickForeground(ctx context.Context) (string, error) {
	var result struct {
		Bundle string `json:"bundleId"`
	}
	err := native.Call(ctx, "app.foreground", nil, &result)
	if err == nil && result.Bundle == "" {
		err = fmt.Errorf("%w: 无法确认前台应用", types.ErrUnknown)
	}
	return result.Bundle, err
}

func foregroundForSnapshot(ctx context.Context, nodes []axNode) string {
	for _, node := range nodes {
		if node.Bundle == "" {
			foreground, _ := clickForeground(ctx)
			return foreground
		}
	}
	return ""
}
