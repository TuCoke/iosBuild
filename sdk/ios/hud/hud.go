// Package hud 对齐 AutoGo iOS 的悬浮显示 API（9 个方法）。
//
// HUD 是"悬浮在 App 之上的一小块文字面板"，脚本跑的时候把状态打在屏幕上。
// 原生侧要一个独立窗口（overlay），所以这里全是桥调用。
//
// 【链式调用】AutoGo 的所有 Set* 都返回 *HUD，照做；Set* 只改本地配置，
// 真正生效在 Show()（避免每次改颜色都重建一次窗口）。
package hud

import (
	"autogov1/v2/sdk/ios/internal/bridge"
)

// TextItem 是一行文本：颜色 + 内容。
type TextItem struct {
	TextColor string // 文字颜色，十六进制格式，例如 "#FFFFFF"
	Text      string // 文本内容
}

// HUD 是悬浮窗对象。
type HUD struct {
	id      string
	items   []TextItem
	x1, y1  int
	x2, y2  int
	bgColor string
	textSize int
	visible bool
}

// New 创建一个 HUD（原生窗口在 Show 时才建）。
func New() *HUD {
	return &HUD{textSize: 14, bgColor: "#00000000"}
}

// SetText 设置文本行。
func (h *HUD) SetText(items []TextItem) *HUD {
	if h == nil {
		return h
	}
	h.items = append([]TextItem(nil), items...)
	return h
}

// SetPosition 设置窗口区域（物理像素，左上-右下）。
func (h *HUD) SetPosition(x1, y1, x2, y2 int) *HUD {
	if h == nil {
		return h
	}
	h.x1, h.y1, h.x2, h.y2 = x1, y1, x2, y2
	return h
}

// SetBackgroundColor 设置背景色（含 alpha，如 "#80000000"）。
func (h *HUD) SetBackgroundColor(color string) *HUD {
	if h == nil {
		return h
	}
	h.bgColor = color
	return h
}

// SetTextSize 设置字号（pt）。
func (h *HUD) SetTextSize(size int) *HUD {
	if h == nil {
		return h
	}
	if size > 0 {
		h.textSize = size
	}
	return h
}

// Show 显示（已显示则更新内容）。
func (h *HUD) Show() {
	if h == nil {
		return
	}
	_ = bridge.Do("hud.show", map[string]any{
		"id": h.id, "items": h.items, "x1": h.x1, "y1": h.y1, "x2": h.x2, "y2": h.y2,
		"background": h.bgColor, "textSize": h.textSize,
	}, nil)
	h.visible = true
}

// Hide 隐藏但不销毁。
func (h *HUD) Hide() {
	if h == nil {
		return
	}
	_ = bridge.Do("hud.hide", map[string]any{"id": h.id}, nil)
	h.visible = false
}

// IsVisible 当前是否显示。
func (h *HUD) IsVisible() bool {
	if h == nil {
		return false
	}
	var out struct {
		Visible bool `json:"visible"`
	}
	if !bridge.Do("hud.visible", map[string]any{"id": h.id}, &out) {
		return h.visible
	}
	return out.Visible
}

// Destroy 销毁原生窗口。
func (h *HUD) Destroy() {
	if h == nil {
		return
	}
	_ = bridge.Do("hud.destroy", map[string]any{"id": h.id}, nil)
	h.visible = false
}
