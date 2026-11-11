//go:build ios && cgo

// Package imgui 是 cimgui（Dear ImGui 的 C API）的 Go 绑定。
//
// 参考样本包含 ig* C 接口和 Metal 后端；当前 vendor 为 1.92.5 / 19250，
// 样本记录为 1.92.5 WIP / 19244，不能仅凭版本名称宣称完全一致。
// 本文件只封装基础窗口、文字、样式与滚动接口；完整 Go 控件绑定仍缺。
//
// 【我们照同一结构】
//   host/ios/third_party/cimgui/        cimgui 当前 vendor（cimgui.h / cimgui.cpp）
//   host/ios/third_party/cimgui/imgui/  imgui 1.92.5（子模块布局）
//   → CI 编成独立 libimgui.dylib，libagovios 用 @rpath 链接
//
// 【为什么这里只 include ag_bridge.h】
// cimgui.h 会拉进 C++ 头（imgui.h / imgui_freetype.h），而 cgo 把 .go 里的
// #include 按 C 编 —— 直接 include cimgui.h 会炸（实测 1639 个错误）。
// 所以：头文件用纯 C 的 ag_bridge.h，实现放 ag_bridge.cpp（cgo 用 CXX 编）。
package imgui

/*
// ${SRCDIR} 由 cgo 展开成【绝对路径】，所以这行不依赖 CI 的工作目录。
// 之前把 -I 写在 CI 的 CGO_CXXFLAGS 里（相对路径），cgo 在包目录下调用编译器，
// 相对路径失效 → ag_bridge.cpp 报 'cimgui.h' file not found。
#cgo CFLAGS: -I${SRCDIR}
#cgo CXXFLAGS: -I${SRCDIR}/../../../host/ios/third_party/cimgui -I${SRCDIR}/../../../host/ios/third_party/cimgui/imgui
#include <stdlib.h>
#include "ag_bridge.h"
*/
import "C"

import "unsafe"

// Vec2 对应 ImVec2（单位是点，不是像素）
type Vec2 struct{ X, Y float32 }

// NewFrame 开始一帧（由原生 Metal 桥每帧调用）
func NewFrame() { C.agImNewFrame() }

// Begin 开窗口；返回 false 表示被裁剪（仍要与 End 配对）
func Begin(name string) bool {
	cs := C.CString(name)
	defer C.free(unsafe.Pointer(cs))
	return C.agImBegin(cs) != 0
}

// End 关窗口
func End() { C.agImEnd() }

// Text 输出一行文本
func Text(s string) {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	C.agImText(cs)
}

// SetNextWindowPos 设置下一个窗口位置
func SetNextWindowPos(v Vec2) { C.agImSetNextWindowPos(C.float(v.X), C.float(v.Y)) }

// SetNextWindowSize 设置下一个窗口尺寸
func SetNextWindowSize(v Vec2) { C.agImSetNextWindowSize(C.float(v.X), C.float(v.Y)) }

// WindowBg 设置窗口背景色（RGBA 各 0-255）
func WindowBg(r, g, b, a uint8) {
	C.agImPushWindowBg(C.uint(uint32(r) | uint32(g)<<8 | uint32(b)<<16 | uint32(a)<<24))
}

// PopStyleColor 与 WindowBg 成对
func PopStyleColor() { C.agImPopStyleColor() }

// ScrollY / ScrollMaxY / SetScrollHereY：控制台自动滚到底用
func ScrollY() float32         { return float32(C.agImScrollY()) }
func ScrollMaxY() float32      { return float32(C.agImScrollMaxY()) }
func SetScrollHereY(c float32) { C.agImSetScrollHereY(C.float(c)) }

// FontSize 当前字体大小（cimgui 没导出 SetWindowFontScale，只能读）
func FontSize() float32 { return float32(C.agImFontSize()) }
