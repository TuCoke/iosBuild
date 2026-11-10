//go:build ios && cgo

// Package imgui 是 cimgui（Dear ImGui 的 C API）的 Go 绑定。
//
// 【依据（逆向 libgoeval.dylib + libimgui.dylib 实测）】
// 参考实现的 libimgui.dylib 导出 6527 个符号，其中 UI 入口是 ig* 这一套：
//   _igNewFrame / _igBegin / _igEnd / _igText / _igSetNextWindowPos /
//   _igSetNextWindowSize / _igPushStyleColor_U32 / _igPopStyleColor …
// 它自己的 libgoeval 导入 4282 个 imgui 符号，Go 侧还有 imgui.Window 这类类型。
// 结论：它是「imgui 源码 + cimgui 生成器产物 → libimgui.dylib」，Go 通过 cgo 调 ig*。
//
// 【我们照同一结构】
//   host/ios/third_party/imgui   imgui v1.93.0（官方源码，MIT）
//   host/ios/third_party/cimgui  cimgui master（生成好的 cimgui.h / cimgui.cpp，MIT）
//   → 编成独立 libimgui.dylib（不是静态库），libagovios 用 @rpath 链接
// 版本必须对齐：cimgui master 对应 imgui 1.93.0（文件头有 `based on imgui.h file version "1.93.0"`）。
package imgui

/*
#cgo CFLAGS: -I${SRCDIR}/../../../host/ios/third_party/cimgui -I${SRCDIR}/../../../host/ios/third_party/cimgui/imgui
#include <stdlib.h>
#include "cimgui.h"
*/
import "C"

import "unsafe"

// Vec2 对应 ImVec2_c
type Vec2 struct{ X, Y float32 }

func toC(v Vec2) C.ImVec2_c { return C.ImVec2_c{C.float(v.X), C.float(v.Y)} }

// NewFrame 开始一帧（由原生 Metal 桥每帧调用）
func NewFrame() { C.igNewFrame() }

// Begin 开窗口；返回 false 表示被裁剪（仍要与 End 配对）
func Begin(name string) bool {
	cs := C.CString(name)
	defer C.free(unsafe.Pointer(cs))
	return bool(C.igBegin(cs, nil, 0))
}

// End 关窗口
func End() { C.igEnd() }

// Text 输出一行文本
func Text(s string) {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	C.igTextUnformatted(cs, nil)
}

// SetNextWindowPos 设置下一个窗口位置（点是逻辑坐标，不是像素）
func SetNextWindowPos(v Vec2) { C.igSetNextWindowPos(toC(v), 0, toC(Vec2{})) }

// SetNextWindowSize 设置下一个窗口尺寸
func SetNextWindowSize(v Vec2) { C.igSetNextWindowSize(toC(v), 0) }

// WindowBg 设置窗口背景色（RGBA 0-255）
func WindowBg(r, g, b, a uint8) {
	C.igPushStyleColor_U32(C.ImGuiCol_WindowBg, C.ImU32(uint32(r)|uint32(g)<<8|uint32(b)<<16|uint32(a)<<24))
}

// PopStyleColor 与 WindowBg 成对
func PopStyleColor() { C.igPopStyleColor(1) }

// FontSize 当前字体大小（cimgui 没导出 SetWindowFontScale，只能读不能设）
func FontSize() float32 { return float32(C.igGetFontSize()) }

// 滚动控制（控制台自动跟到底）
func ScrollY() float32          { return float32(C.igGetScrollY()) }
func ScrollMaxY() float32       { return float32(C.igGetScrollMaxY()) }
func SetScrollHereY(c float32)  { C.igSetScrollHereY(C.float(c)) }
