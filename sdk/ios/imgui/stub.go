//go:build !ios || !cgo

// Package imgui 在非 iOS / 无 cgo 环境下是空实现：
// 保持可导入，但所有调用都是空操作（悬浮层本来也只在设备上有意义）。
package imgui

// Vec2 与 cimgui 的 ImVec2 对应
type Vec2 struct{ X, Y float32 }

func NewFrame()                        {}
func Begin(name string, open *bool) bool { return false }
func End()                             {}
func Text(s string)                    {}
func SetNextWindowPos(v Vec2)          {}
func SetNextWindowSize(v Vec2)         {}
func WindowBg(r, g, b, a uint8)        {}
func PopStyleColor()                   {}
