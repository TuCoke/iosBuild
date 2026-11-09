// Package uiacc 提供控件快照查询。原生 AX 采集通过 Provider 接入，不猜测私有属性编号。
package uiacc

import (
	"context"
	"fmt"
	"time"

	"autogov1/v2/sdk/ios/types"
)

type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// 状态使用指针区分“否”和“当前采集器未提供”，避免把未知状态当作 false。
type Node struct {
	ID       string  `json:"id"`
	Text     string  `json:"text"`
	Value    string  `json:"value"`
	Role     string  `json:"role"`
	Bounds   Rect    `json:"bounds"`
	Enabled  *bool   `json:"enabled,omitempty"`
	Visible  *bool   `json:"visible,omitempty"`
	Children []*Node `json:"children,omitempty"`
}

// Snapshot 的 Bounds 坐标是当前截图像素，采集器必须给出同方向的屏幕尺寸。
type Snapshot struct {
	Roots    []*Node `json:"roots"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Rotation int     `json:"rotation"`
}
type Provider interface {
	Snapshot(context.Context) (Snapshot, error)
}
type Query struct {
	ID, Text, Role string
	Predicate      func(*Node) bool
}

func (q Query) matches(n *Node) bool {
	return (q.ID == "" || n.ID == q.ID) && (q.Text == "" || n.Text == q.Text) && (q.Role == "" || n.Role == q.Role) && (q.Predicate == nil || q.Predicate(n))
}

// Find 使用迭代遍历并限制节点总数，同时防止采集器生成循环引用。
func Find(s Snapshot, q Query) ([]*Node, error) {
	if len(s.Roots) > 20000 {
		return nil, fmt.Errorf("%w: 控件树根节点过多", types.ErrInvalid)
	}
	stack := append([]*Node(nil), s.Roots...)
	seen := map[*Node]bool{}
	var found []*Node
	for len(stack) > 0 {
		n := stack[0]
		stack = stack[1:]
		if n == nil || seen[n] {
			continue
		}
		seen[n] = true
		if len(seen) > 20000 {
			return nil, fmt.Errorf("%w: 控件树过大", types.ErrInvalid)
		}
		if q.matches(n) {
			found = append(found, n)
		}
		if len(stack)+len(n.Children) > 20000 {
			return nil, fmt.Errorf("%w: 控件树待处理节点过多", types.ErrInvalid)
		}
		stack = append(stack, n.Children...)
	}
	return found, nil
}
func Wait(ctx context.Context, p Provider, q Query, interval time.Duration) (*Node, error) {
	if ctx == nil || p == nil || interval < 20*time.Millisecond {
		return nil, types.ErrInvalid
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s, err := p.Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		nodes, err := Find(s, q)
		if err != nil {
			return nil, err
		}
		if len(nodes) > 0 {
			return nodes[0], nil
		}
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}
