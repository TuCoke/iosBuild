//go:build !ios || !cgo

// Package imgui 在非 iOS / 无 cgo 环境下是空实现：保持可导入，调用无副作用。
package imgui

// Vec2 与 iOS 侧的 AgImVec2 对应
type Vec2 struct{ X, Y float32 }

func NewFrame()                        {}
func Begin(name string) bool           { return false }
func End()                             {}
func Text(s string)                    {}
func SetNextWindowPos(v Vec2)          {}
func SetNextWindowSize(v Vec2)         {}
func WindowBg(r, g, b, a uint8)        {}
func PopStyleColor()                   {}
func SetFontScale(scale float32)       {}
func ScrollY() float32                 { return 0 }
func ScrollMaxY() float32              { return 0 }
func SetScrollHereY(center float32)    {}
