//go:build !ios || !cgo

// Package imgui 在非 iOS / 无 cgo 环境下可导入；调用绘制方法明确报不支持。
package imgui

import "autogov1/v2/sdk/ios/types"

// Vec2 与 iOS 侧的 AgImVec2 对应
type Vec2 struct{ X, Y float32 }

func NewFrame()                     { panic(types.ErrUnsupported) }
func Begin(name string) bool        { panic(types.ErrUnsupported) }
func End()                          { panic(types.ErrUnsupported) }
func Text(s string)                 { panic(types.ErrUnsupported) }
func SetNextWindowPos(v Vec2)       { panic(types.ErrUnsupported) }
func SetNextWindowSize(v Vec2)      { panic(types.ErrUnsupported) }
func WindowBg(r, g, b, a uint8)     { panic(types.ErrUnsupported) }
func PopStyleColor()                { panic(types.ErrUnsupported) }
func FontSize() float32             { panic(types.ErrUnsupported) }
func ScrollY() float32              { panic(types.ErrUnsupported) }
func ScrollMaxY() float32           { panic(types.ErrUnsupported) }
func SetScrollHereY(center float32) { panic(types.ErrUnsupported) }
