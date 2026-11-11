package uiacc

import (
	"context"
	"testing"
)

// fakeProvider 让选择器逻辑可以在不接原生的情况下测。
type fakeProvider struct {
	snapshot Snapshot
	err      error
	calls    int
}

func (f *fakeProvider) Snapshot(context.Context) (Snapshot, error) {
	f.calls++
	return f.snapshot, f.err
}

func boolPtr(v bool) *bool { return &v }

// 构造一棵树：root → [child1(text=登录), child2(text=注册) → grand(登录)]
func testSnapshot() Snapshot {
	child1 := &Node{ID: "btn-login", Text: "登录", Role: "Button",
		Bounds: Bounds{X: 10, Y: 20, Width: 100, Height: 50}, Enabled: boolPtr(true)}
	child2 := &Node{ID: "btn-signup", Text: "注册", Role: "Button",
		Bounds: Bounds{X: 10, Y: 80, Width: 100, Height: 50}, Enabled: boolPtr(false)}
	grand := &Node{ID: "link-login", Text: "登录", Role: "Link",
		Bounds: Bounds{X: 20, Y: 140, Width: 80, Height: 20}}
	child2.Children = []*Node{grand}
	root := &Node{ID: "root", Text: "", Role: "Window",
		Bounds:   Bounds{X: 0, Y: 0, Width: 320, Height: 640},
		Children: []*Node{child1, child2}}
	return Snapshot{Roots: []*Node{root}}
}

func TestFindByTextAndRole(t *testing.T) {
	s := testSnapshot()
	got, err := Find(s, Query{Text: "登录"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("按 text 找登录应有 2 个，实际 %d", len(got))
	}
	got, _ = Find(s, Query{Text: "登录", Role: "Link"})
	if len(got) != 1 || got[0].ID != "link-login" {
		t.Fatalf("按 text+role 过滤错: %v", got)
	}
	got, _ = Find(s, Query{ID: "btn-signup"})
	if len(got) != 1 {
		t.Fatalf("按 ID 找应命中 1 个，实际 %d", len(got))
	}
	if got, _ := Find(s, Query{Text: "不存在"}); len(got) != 0 {
		t.Fatalf("不该命中，实际 %d", len(got))
	}
}

func TestFindWithPredicate(t *testing.T) {
	s := testSnapshot()
	got, err := Find(s, Query{Predicate: func(n *Node) bool {
		return n.Enabled != nil && *n.Enabled
	}})
	if err != nil {
		t.Fatal(err)
	}
	// root 没设 Enabled；child1 true；child2 false；grand 未设
	if len(got) != 1 || got[0].ID != "btn-login" {
		t.Fatalf("谓词过滤错: %v", len(got))
	}
}

// 环不能把遍历卡死：节点互相引用时必须能正常返回。
func TestFindHandlesCycle(t *testing.T) {
	a := &Node{ID: "a"}
	b := &Node{ID: "b"}
	a.Children = []*Node{b}
	b.Children = []*Node{a}
	got, err := Find(Snapshot{Roots: []*Node{a}}, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("有环时应各访问一次，实际 %d", len(got))
	}
}

// 根节点过多要拒绝，避免一次快照把内存打爆。
func TestFindRejectsOversizedSnapshot(t *testing.T) {
	roots := make([]*Node, 20001)
	for i := range roots {
		roots[i] = &Node{ID: "n"}
	}
	if _, err := Find(Snapshot{Roots: roots}, Query{}); err == nil {
		t.Fatal("超量快照应报错")
	}
}
