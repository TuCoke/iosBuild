// Package console 对齐 AutoGo iOS 的设备端控制台 API（12 个方法）。
//
// 它是脚本在手机上自己开的那个日志窗口（Image/ImGui 画出来的），和我们的
// 服务端控制台是两回事：那个给人远程看，这个给"手机上现场看"。
package console

import (
	"fmt"

	"autogov1/v2/sdk/ios/internal/bridge"
)

// Console 是设备端控制台窗口。
type Console struct {
	id       string
	width    int
	height   int
	x, y     int
	bgColor  string
	textColor string
	textSize int
	visible  bool
	lines    []string
}

// New 创建设备端控制台。
func New() *Console {
	return &Console{textSize: 13, textColor: "#FFFFFF", bgColor: "#CC000000"}
}

// SetWindowSize 设置窗口尺寸（像素）。
func (c *Console) SetWindowSize(width, height int) *Console {
	if c == nil {
		return c
	}
	if width > 0 {
		c.width = width
	}
	if height > 0 {
		c.height = height
	}
	return c
}

// SetWindowPosition 设置窗口位置（像素）。
func (c *Console) SetWindowPosition(x, y int) *Console {
	if c == nil {
		return c
	}
	c.x, c.y = x, y
	return c
}

// SetWindowColor 设置窗口背景色（含 alpha）。
func (c *Console) SetWindowColor(color string) *Console {
	if c == nil {
		return c
	}
	c.bgColor = color
	return c
}

// SetTextColor 设置文字颜色。
func (c *Console) SetTextColor(color string) *Console {
	if c == nil {
		return c
	}
	c.textColor = color
	return c
}

// SetTextSize 设置字号。
func (c *Console) SetTextSize(size int) *Console {
	if c == nil {
		return c
	}
	if size > 0 {
		c.textSize = size
	}
	return c
}

// Println 追加一行日志（本地留一份，原生不可用时不至于什么都看不到）。
func (c *Console) Println(a ...any) {
	if c == nil {
		return
	}
	line := fmt.Sprint(a...)
	c.lines = append(c.lines, line)
	if len(c.lines) > 500 {
		c.lines = c.lines[len(c.lines)-500:]
	}
	_ = bridge.Do("console.print", map[string]any{"id": c.id, "text": line}, nil)
}

// Clear 清空窗口内容。
func (c *Console) Clear() {
	if c == nil {
		return
	}
	c.lines = nil
	_ = bridge.Do("console.clear", map[string]any{"id": c.id}, nil)
}

// Show 显示窗口（首次显示时把配置一起下发）。
func (c *Console) Show() {
	if c == nil {
		return
	}
	_ = bridge.Do("console.show", map[string]any{
		"id": c.id, "width": c.width, "height": c.height, "x": c.x, "y": c.y,
		"background": c.bgColor, "textColor": c.textColor, "textSize": c.textSize,
		"lines": c.lines,
	}, nil)
	c.visible = true
}

// Hide 隐藏。
func (c *Console) Hide() {
	if c == nil {
		return
	}
	_ = bridge.Do("console.hide", map[string]any{"id": c.id}, nil)
	c.visible = false
}

// IsVisible 当前是否显示。
func (c *Console) IsVisible() bool {
	if c == nil {
		return false
	}
	var out struct {
		Visible bool `json:"visible"`
	}
	if !bridge.Do("console.visible", map[string]any{"id": c.id}, &out) {
		return c.visible
	}
	return out.Visible
}

// Destroy 销毁窗口。
func (c *Console) Destroy() {
	if c == nil {
		return
	}
	_ = bridge.Do("console.destroy", map[string]any{"id": c.id}, nil)
	c.visible = false
}
