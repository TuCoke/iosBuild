//go:build ios && cgo

// Package imgui 是 cimgui（imgui 的 C API）的 Go 绑定。
//
// 【为什么用 cimgui 而不是 ObjC++ 直调 ImGui::】
// 参考实现就是这么做的：它的 libgoeval.dylib 里 igBegin 出现 256 次、
// igEnd 127 次、igNewFrame 4 次，并带一堆 imgui.Window / imgui.Vec2 这类
// Go 类型的符号 —— 说明悬浮层是 Go 通过 cgo 调 cimgui 画的，不是 ObjC++。
// 我们照同一形态实现，行为/分层才和它一致。
//
// 【渲染后端仍在原生】
// cimgui 只管"画什么"，"画到哪"由原生 Metal 桥负责（AGImGuiRenderFrame），
// 所以这里的函数只覆盖 UI 构建，不含渲染。
package imgui

/*
#cgo CFLAGS: -I${SRCDIR}/../../../host/ios/third_party/imgui -I${SRCDIR}/../../../host/ios/third_party/cimgui
#include "cimgui.h"
*/
import "C"

import "unsafe"

// Vec2 对应 ImVec2_c
type Vec2 struct{ X, Y float32 }

// toC 用位置初始化：cgo 生成的 Go 结构体不保证支持字段名，位置写法在
// cimgui 的 ImVec2_c（float x, y）上稳定可用。
func toC(v Vec2) C.ImVec2_c { return C.ImVec2_c{C.float(v.X), C.float(v.Y)} }

// NewFrame 开始一帧（渲染桥在原生里调用，Go 侧一般不需要直接调）
func NewFrame() { C.igNewFrame() }

// Begin 开一个窗口。返回 false 表示窗口被裁剪，应与 End 成对调用。
func Begin(name string, open *bool) bool {
	cs := C.CString(name)
	defer C.free(unsafe.Pointer(cs))
	var pOpen *C.bool
	if open != nil {
		v := C.bool(*open)
		pOpen = &v
	}
	return bool(C.igBegin(cs, pOpen, 0))
}

// End 关闭窗口
func End() { C.igEnd() }

// Text 输出一行文本
func Text(s string) {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	C.igText("%s", cs)
}

// SetNextWindowPos 设置下一个窗口位置
func SetNextWindowPos(v Vec2) { C.igSetNextWindowPos(toC(v), 0, toC(Vec2{})) }

// SetNextWindowSize 设置下一个窗口尺寸
func SetNextWindowSize(v Vec2) { C.igSetNextWindowSize(toC(v), 0) }

// WindowBg 设置窗口背景色（RGBA 0-255）
func WindowBg(r, g, b, a uint8) {
	C.igPushStyleColor_U32(C.ImGuiCol_WindowBg, C.ImU32(uint32(r)<<24|uint32(g)<<16|uint32(b)<<8|uint32(a)))
}

// PopStyleColor 与 WindowBg 成对
func PopStyleColor() { C.igPopStyleColor(1) }
