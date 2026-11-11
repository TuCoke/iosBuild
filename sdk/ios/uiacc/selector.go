// 控件选择器与节点快照。
//
// 数据来源是原生 AX 采集器；这里是纯 Go 的选择器与节点封装 ——
// 原生只负责给一份节点数组，过滤/等待/点击换算都在这一层完成。
//
// 【为什么过滤放 Go 侧】
//   · 一次采集能服务多个选择器
//   · 正则/前缀/包含这些判断可测，改匹配规则不必重编原生

package uiacc

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"autogov1/v2/sdk/ios/motion"
	"autogov1/v2/sdk/ios/types"
)

// Rect 与 Android 版字段完全对齐，单位是物理像素。
type Rect struct {
	Left    int
	Right   int
	Top     int
	Bottom  int
	CenterX int
	CenterY int
	Width   int
	Height  int
}

// axNode 是原生返回的扁平节点。字段名与 AutoGo 的语义一一对应：
//   - identifier      → Id
//   - value           → Desc（AutoGo 把 accessibilityValue 对齐 contentDescription）
//   - role / class2   → ClassName（class2 为空时回退 role）
type axNode struct {
	Text       string `json:"text"`
	Value      string `json:"value"`
	Identifier string `json:"identifier"`
	Class      string `json:"class"`
	Role       string `json:"role"`
	Bundle     string `json:"bundle"`
	Left       int    `json:"left"`
	Top        int    `json:"top"`
	Right      int    `json:"right"`
	Bottom     int    `json:"bottom"`
	ParentL    int    `json:"parentLeft"`
	ParentT    int    `json:"parentTop"`
	ParentR    int    `json:"parentRight"`
	ParentB    int    `json:"parentBottom"`
	Index      int    `json:"index"`
	Children   int    `json:"childCount"`
	Clickable  *bool  `json:"clickable"`
	Selected   *bool  `json:"selected"`
	Enabled    *bool  `json:"enabled"`
	Scrollable *bool  `json:"scrollable"`
	Editable   *bool  `json:"editable"`
	Checked    *bool  `json:"checked"`
	Password   *bool  `json:"password"`
}

// Uiacc 是一个选择器：每个方法都是"加一个条件"，链式直到 Find/WaitFor/Click。
type Uiacc struct {
	mu sync.Mutex
	// 文本类条件：mode 0=等于 1=包含 2=前缀 3=后缀 4=正则
	text, desc, id, class, pkg string

	textMode, descMode, idMode, classMode, pkgMode int

	bounds, boundsInside, boundsContains *Rect

	clickable, selected, enabled, scrollable, editable, checked, password *bool

	index *int

	released bool
	lease    *snapshotLoop
	lastErr  error
	life     context.Context
	cancel   context.CancelFunc
}

// New 创建一个选择器实例。
func New() *Uiacc { return NewWithContext(context.Background()) }

// NewWithContext 将选择器引用绑定到任务；任务结束自动释放，避免脚本漏写 Release 后持续采集。
func NewWithContext(ctx context.Context) *Uiacc {
	if ctx == nil {
		return &Uiacc{released: true, lastErr: types.ErrInvalid}
	}
	if err := ctx.Err(); err != nil {
		return &Uiacc{released: true, lastErr: err}
	}
	life, cancel := context.WithCancel(ctx)
	u := &Uiacc{lease: sharedSnapshots.acquire(), life: life, cancel: cancel}
	context.AfterFunc(life, u.Release)
	return u
}

// Release 释放引用；之后的选择器调用无效。
func (u *Uiacc) Release() {
	if u == nil {
		return
	}
	u.mu.Lock()
	if u.released {
		u.mu.Unlock()
		return
	}
	u.released = true
	lease := u.lease
	u.mu.Unlock()
	sharedSnapshots.release(lease)
	if u.cancel != nil {
		u.cancel()
	}
}

// ---- 文本 / 描述 / id / 类名 / 包名（各 5 种匹配）----

func (u *Uiacc) Text(v string) *Uiacc           { u.setText(&u.text, &u.textMode, v, 0); return u }
func (u *Uiacc) TextContains(v string) *Uiacc   { u.setText(&u.text, &u.textMode, v, 1); return u }
func (u *Uiacc) TextStartsWith(v string) *Uiacc { u.setText(&u.text, &u.textMode, v, 2); return u }
func (u *Uiacc) TextEndsWith(v string) *Uiacc   { u.setText(&u.text, &u.textMode, v, 3); return u }
func (u *Uiacc) TextMatches(p string) *Uiacc    { u.setText(&u.text, &u.textMode, p, 4); return u }

func (u *Uiacc) Desc(v string) *Uiacc           { u.setText(&u.desc, &u.descMode, v, 0); return u }
func (u *Uiacc) DescContains(v string) *Uiacc   { u.setText(&u.desc, &u.descMode, v, 1); return u }
func (u *Uiacc) DescStartsWith(v string) *Uiacc { u.setText(&u.desc, &u.descMode, v, 2); return u }
func (u *Uiacc) DescEndsWith(v string) *Uiacc   { u.setText(&u.desc, &u.descMode, v, 3); return u }
func (u *Uiacc) DescMatches(p string) *Uiacc    { u.setText(&u.desc, &u.descMode, p, 4); return u }

func (u *Uiacc) Id(v string) *Uiacc           { u.setText(&u.id, &u.idMode, v, 0); return u }
func (u *Uiacc) IdContains(v string) *Uiacc   { u.setText(&u.id, &u.idMode, v, 1); return u }
func (u *Uiacc) IdStartsWith(v string) *Uiacc { u.setText(&u.id, &u.idMode, v, 2); return u }
func (u *Uiacc) IdEndsWith(v string) *Uiacc   { u.setText(&u.id, &u.idMode, v, 3); return u }
func (u *Uiacc) IdMatches(p string) *Uiacc    { u.setText(&u.id, &u.idMode, p, 4); return u }

func (u *Uiacc) ClassName(v string) *Uiacc         { u.setText(&u.class, &u.classMode, v, 0); return u }
func (u *Uiacc) ClassNameContains(v string) *Uiacc { u.setText(&u.class, &u.classMode, v, 1); return u }
func (u *Uiacc) ClassNameStartsWith(v string) *Uiacc {
	u.setText(&u.class, &u.classMode, v, 2)
	return u
}
func (u *Uiacc) ClassNameEndsWith(v string) *Uiacc { u.setText(&u.class, &u.classMode, v, 3); return u }
func (u *Uiacc) ClassNameMatches(p string) *Uiacc  { u.setText(&u.class, &u.classMode, p, 4); return u }

func (u *Uiacc) PackageName(v string) *Uiacc           { u.setText(&u.pkg, &u.pkgMode, v, 0); return u }
func (u *Uiacc) PackageNameContains(v string) *Uiacc   { u.setText(&u.pkg, &u.pkgMode, v, 1); return u }
func (u *Uiacc) PackageNameStartsWith(v string) *Uiacc { u.setText(&u.pkg, &u.pkgMode, v, 2); return u }
func (u *Uiacc) PackageNameEndsWith(v string) *Uiacc   { u.setText(&u.pkg, &u.pkgMode, v, 3); return u }
func (u *Uiacc) PackageNameMatches(p string) *Uiacc    { u.setText(&u.pkg, &u.pkgMode, p, 4); return u }

func (u *Uiacc) setText(dst *string, mode *int, v string, m int) {
	if u == nil || v == "" {
		return
	}
	u.mu.Lock()
	*dst, *mode = v, m
	u.mu.Unlock()
}

// ---- 区域与布尔属性 ----

func (u *Uiacc) Bounds(left, top, right, bottom int) *Uiacc {
	u.setRect(&u.bounds, left, top, right, bottom)
	return u
}
func (u *Uiacc) BoundsInside(left, top, right, bottom int) *Uiacc {
	u.setRect(&u.boundsInside, left, top, right, bottom)
	return u
}
func (u *Uiacc) BoundsContains(left, top, right, bottom int) *Uiacc {
	u.setRect(&u.boundsContains, left, top, right, bottom)
	return u
}

func (u *Uiacc) setRect(dst **Rect, l, t, r, b int) {
	if u == nil {
		return
	}
	u.mu.Lock()
	*dst = &Rect{Left: l, Top: t, Right: r, Bottom: b, Width: r - l, Height: b - t,
		CenterX: (l + r) / 2, CenterY: (t + b) / 2}
	u.mu.Unlock()
}

func (u *Uiacc) Clickable(v bool) *Uiacc  { u.setBool(&u.clickable, v); return u }
func (u *Uiacc) Selected(v bool) *Uiacc   { u.setBool(&u.selected, v); return u }
func (u *Uiacc) Enabled(v bool) *Uiacc    { u.setBool(&u.enabled, v); return u }
func (u *Uiacc) Scrollable(v bool) *Uiacc { u.setBool(&u.scrollable, v); return u }
func (u *Uiacc) Editable(v bool) *Uiacc   { u.setBool(&u.editable, v); return u }
func (u *Uiacc) Checked(v bool) *Uiacc    { u.setBool(&u.checked, v); return u }
func (u *Uiacc) Password(v bool) *Uiacc   { u.setBool(&u.password, v); return u }

func (u *Uiacc) setBool(dst **bool, v bool) {
	if u == nil {
		return
	}
	u.mu.Lock()
	*dst = &v
	u.mu.Unlock()
}

func (u *Uiacc) Index(v int) *Uiacc {
	if u == nil {
		return u
	}
	u.mu.Lock()
	u.index = &v
	u.mu.Unlock()
	return u
}

// ---- 查找与等待 ----

// Error 返回最近一次查找的错误，区分“没匹配”与“采集失败”。
func (u *Uiacc) Error() error {
	if u == nil {
		return types.ErrClosed
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.lastErr
}
func (u *Uiacc) snapshot(ctx context.Context) ([]axNode, error) {
	if u == nil {
		return nil, types.ErrClosed
	}
	u.mu.Lock()
	released, lease, life := u.released, u.lease, u.life
	u.mu.Unlock()
	if released {
		return nil, types.ErrClosed
	}
	if lease == nil {
		return nil, fmt.Errorf("请使用 uiacc.New 创建选择器")
	}
	if life == nil {
		return lease.snapshot(ctx)
	}
	merged, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(life, cancel)
	defer stop()
	defer cancel()
	return lease.snapshot(merged)
}

// match 判断单个节点是否满足当前全部条件。
func (u *Uiacc) match(n *axNode) bool {
	okText := func(value string, want string, mode int) bool {
		if want == "" {
			return true
		}
		switch mode {
		case 1:
			return strings.Contains(value, want)
		case 2:
			return strings.HasPrefix(value, want)
		case 3:
			return strings.HasSuffix(value, want)
		case 4:
			re, err := regexp.Compile(want)
			return err == nil && re.MatchString(value)
		default:
			return value == want
		}
	}
	if !okText(n.Text, u.text, u.textMode) || !okText(n.Value, u.desc, u.descMode) ||
		!okText(n.Identifier, u.id, u.idMode) || !okText(className(n), u.class, u.classMode) ||
		!okText(n.Bundle, u.pkg, u.pkgMode) {
		return false
	}
	if u.bounds != nil && !(n.Left == u.bounds.Left && n.Top == u.bounds.Top && n.Right == u.bounds.Right && n.Bottom == u.bounds.Bottom) {
		return false
	}
	if u.boundsInside != nil && !(n.Left >= u.boundsInside.Left && n.Top >= u.boundsInside.Top &&
		n.Right <= u.boundsInside.Right && n.Bottom <= u.boundsInside.Bottom) {
		return false
	}
	if u.boundsContains != nil && !(n.Left <= u.boundsContains.Left && n.Top <= u.boundsContains.Top &&
		n.Right >= u.boundsContains.Right && n.Bottom >= u.boundsContains.Bottom) {
		return false
	}
	for _, pair := range []struct {
		want *bool
		got  *bool
	}{
		{u.clickable, n.Clickable}, {u.selected, n.Selected}, {u.enabled, n.Enabled},
		{u.scrollable, n.Scrollable}, {u.editable, n.Editable}, {u.checked, n.Checked},
		{u.password, n.Password},
	} {
		if pair.want != nil && (pair.got == nil || *pair.want != *pair.got) {
			return false
		}
	}
	if u.index != nil && n.Index != *u.index {
		return false
	}
	return true
}

func className(n *axNode) string {
	if n.Class != "" {
		return n.Class
	}
	return n.Role
}

// Click 先按 text 匹配、再按 desc 匹配（与 AutoGo 一致），命中即点。
func (u *Uiacc) Click(text string) bool {
	if u == nil || text == "" {
		return false
	}
	// 两次查询各自加条件，不把 Text 与 Desc 同时积累到原选择器上。
	nodes, err := u.snapshot(context.Background())
	if err != nil {
		u.mu.Lock()
		u.lastErr = err
		u.mu.Unlock()
		return false
	}
	u.mu.Lock()
	u.lastErr = nil
	if u.released {
		u.lastErr = types.ErrClosed
		u.mu.Unlock()
		return false
	}
	var selected *UiObject
	for _, desc := range []bool{false, true} {
		for i := range nodes {
			if u.match(&nodes[i]) && ((!desc && nodes[i].Text == text) || (desc && nodes[i].Value == text)) {
				selected = &UiObject{node: nodes[i]}
				break
			}
		}
		if selected != nil {
			break
		}
	}
	u.mu.Unlock()
	if selected == nil {
		return false
	}
	ctx := u.life
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = selected.ClickContext(ctx)
	u.mu.Lock()
	u.lastErr = err
	u.mu.Unlock()
	return err == nil
}

// WaitFor 的毫秒参数保持不变；新增带 context 入口用于任务取消。
func (u *Uiacc) WaitFor(timeout int) *UiObject {
	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
		defer cancel()
	}
	obj, _ := u.WaitForContext(ctx)
	return obj
}
func (u *Uiacc) WaitForContext(ctx context.Context) (*UiObject, error) {
	if ctx == nil {
		return nil, fmt.Errorf("查找 context 不能为空")
	}
	for {
		all, err := u.FindContext(ctx)
		if err != nil {
			return nil, err
		}
		if len(all) > 0 {
			return all[0], nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		u.mu.Lock()
		lease, life := u.lease, u.life
		u.mu.Unlock()
		var lifeDone <-chan struct{}
		if life != nil {
			lifeDone = life.Done()
		}
		if lease == nil {
			timer.Stop()
			return nil, types.ErrClosed
		}
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-lifeDone:
			timer.Stop()
			return nil, types.ErrClosed
		case <-lease.ctx.Done():
			timer.Stop()
			return nil, types.ErrClosed
		case <-timer.C:
		}
	}
}
func (u *Uiacc) FindOnce() *UiObject {
	all := u.Find()
	if len(all) == 0 {
		return nil
	}
	return all[0]
}
func (u *Uiacc) Find() []*UiObject { all, _ := u.FindContext(context.Background()); return all }
func (u *Uiacc) FindContext(ctx context.Context) ([]*UiObject, error) {
	if ctx == nil {
		return nil, fmt.Errorf("查找 context 不能为空")
	}
	nodes, err := u.snapshot(ctx)
	if u == nil {
		return nil, err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.lastErr = err
	if err != nil {
		return nil, err
	}
	if u.released {
		u.lastErr = types.ErrClosed
		return nil, u.lastErr
	}
	out := make([]*UiObject, 0, 4)
	for i := range nodes {
		if u.match(&nodes[i]) {
			out = append(out, &UiObject{node: nodes[i]})
		}
	}
	return out, nil
}

// UiObject 是查询瞬间的节点快照；后续页面变化不会更新它。
type UiObject struct {
	node axNode
}

// Click 请求点击控件中心，返回动作调用是否成功；业务结果仍需另行验证。
func (n *UiObject) Click() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return n.ClickContext(ctx) == nil
}

// ClickContext 保留原生错误；返回成功表示动作调用完成，不能代替业务成功验证。
func (n *UiObject) ClickContext(ctx context.Context) error {
	if n == nil {
		return types.ErrInvalid
	}
	x, y := n.node.CenterX()
	if n.node.Right <= n.node.Left || n.node.Bottom <= n.node.Top {
		return types.ErrInvalid
	}
	return motion.ClickContext(ctx, x, y, 0, 20)
}

// CenterX / CenterY 在 Rect 之外给节点用（保持在 uiacc 内部，不污染公开 Rect）。
func (n *axNode) CenterX() (int, int) {
	return (n.Left + n.Right) / 2, (n.Top + n.Bottom) / 2
}

func rectOf(l, t, r, b int) Rect {
	return Rect{Left: l, Top: t, Right: r, Bottom: b, Width: r - l, Height: b - t,
		CenterX: (l + r) / 2, CenterY: (t + b) / 2}
}

func (n *UiObject) GetBounds() Rect {
	if n == nil {
		return Rect{}
	}
	return rectOf(n.node.Left, n.node.Top, n.node.Right, n.node.Bottom)
}

func (n *UiObject) GetBoundsInParent() Rect {
	if n == nil {
		return Rect{}
	}
	return rectOf(n.node.ParentL, n.node.ParentT, n.node.ParentR, n.node.ParentB)
}

func (n *UiObject) GetId() string {
	if n == nil {
		return ""
	}
	return n.node.Identifier
}

func (n *UiObject) GetText() string {
	if n == nil {
		return ""
	}
	return n.node.Text
}

func (n *UiObject) GetDesc() string {
	if n == nil {
		return ""
	}
	return n.node.Value
}

func (n *UiObject) GetPackageName() string {
	if n == nil {
		return ""
	}
	return n.node.Bundle
}

func (n *UiObject) GetClassName() string {
	if n == nil {
		return ""
	}
	return className(&n.node)
}

func (n *UiObject) GetClickable() bool {
	return n != nil && n.node.Clickable != nil && *n.node.Clickable
}
func (n *UiObject) GetSelected() bool { return n != nil && n.node.Selected != nil && *n.node.Selected }
func (n *UiObject) GetEnabled() bool  { return n != nil && n.node.Enabled != nil && *n.node.Enabled }
func (n *UiObject) GetScrollable() bool {
	return n != nil && n.node.Scrollable != nil && *n.node.Scrollable
}
func (n *UiObject) GetEditable() bool { return n != nil && n.node.Editable != nil && *n.node.Editable }
func (n *UiObject) GetChecked() bool  { return n != nil && n.node.Checked != nil && *n.node.Checked }
func (n *UiObject) GetPassword() bool { return n != nil && n.node.Password != nil && *n.node.Password }

func (n *UiObject) GetChildCount() int {
	if n == nil {
		return 0
	}
	return n.node.Children
}

func (n *UiObject) GetIndex() int {
	if n == nil {
		return 0
	}
	return n.node.Index
}

// ToString 把节点打成一行文本，便于日志里直接看。
func (n *UiObject) ToString() string {
	if n == nil {
		return ""
	}
	return "text=" + n.node.Text + " desc=" + n.node.Value + " id=" + n.node.Identifier +
		" class=" + className(&n.node) + " bounds=" + itoa(n.node.Left) + "," + itoa(n.node.Top) +
		"," + itoa(n.node.Right) + "," + itoa(n.node.Bottom)
}

func itoa(v int) string {
	// 小工具，避免为一个数字引入 fmt（这一包已经很轻）。
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	buf := [12]byte{}
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
