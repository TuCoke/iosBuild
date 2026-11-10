//go:build ios && cgo

// Package imgui 是悬浮层用的 imgui 绑定。
//
// 【形态与参考实现一致】
// AutoGo 的 libgoeval 里 igBegin 出现 256 次、igEnd 127 次，且带 imgui.Window
// 这类 Go 类型符号 —— 它是 Go 通过 cgo 调一层 C API 来画 UI。
// 我们也走同一条：Go → cgo → C 函数 → ImGui::
//
// 【为什么不用官方 cimgui】
// 官方 cimgui 的 master 对应 imgui 1.92+（有 ImVector_ImWchar 等新类型），
// 与我们 vendor 的 1.91.5 不兼容，实测 1745 个编译错误。
// 所以自己写了 10 个函数的薄 shim（host/ios/third_party/imgui_shim）。
//
// 【渲染后端在原生】
// 这层只管"画什么"；"画到哪"由原生 Metal 桥（AGImGuiRenderFrame）负责。
package imgui

/*
#cgo CFLAGS: -I${SRCDIR}/../../../host/ios/third_party/imgui_shim -I${SRCDIR}/../../../host/ios/third_party/imgui
#include "ag_imgui_c.h"
*/
import "C"

import "unsafe"

// Vec2 对应 C 侧的 AgImVec2
type Vec2 struct{ X, Y float32 }

// NewFrame 开始一帧
func NewFrame() { C.agImNewFrame() }

// Begin 开窗口；返回 false 表示被裁剪（仍要与 End 配对）
func Begin(name string) bool {
	cs := C.CString(name)
	defer C.free(unsafe.Pointer(cs))
	return C.agImBegin(cs, 0) != 0
}

// End 关窗口
func End() { C.agImEnd() }

// Text 输出一行文本
func Text(s string) {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	C.agImText(cs)
}

// SetNextWindowPos 设置下一个窗口位置（点，不是像素）
func SetNextWindowPos(v Vec2) { C.agImSetNextWindowPos(C.float(v.X), C.float(v.Y)) }

// SetNextWindowSize 设置下一个窗口尺寸
func SetNextWindowSize(v Vec2) { C.agImSetNextWindowSize(C.float(v.X), C.float(v.Y)) }

// WindowBg 设置窗口背景（RGBA，0-255）
func WindowBg(r, g, b, a uint8) {
	C.agImPushWindowBg(C.uint(uint32(a)<<24 | uint32(b)<<16 | uint32(g)<<8 | uint32(r)))
}

// PopStyleColor 与 WindowBg 成对
func PopStyleColor() { C.agImPopStyleColor() }

// SetFontScale 字号缩放（1.0 = 默认）
func SetFontScale(scale float32) { C.agImSetWindowFontScale(C.float(scale)) }

// ScrollY / ScrollMaxY 用于控制台自动滚动到底
func ScrollY() float32 { return float32(C.agImGetScrollY()) }
func ScrollMaxY() float32 { return float32(C.agImGetScrollMaxY()) }
func SetScrollHereY(center float32) { C.agImSetScrollHereY(C.float(center)) }