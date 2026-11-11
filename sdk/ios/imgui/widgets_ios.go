//go:build ios && cgo

package imgui

/*
#include <stdlib.h>
#include <string.h>
#include "ag_bridge.h"
extern void agImGoDraw(void);
static inline void agImInstallGoDraw(void) { agImSetDrawCallback(agImGoDraw); }
*/
import "C"

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/types"
)

var drawState struct {
	sync.Mutex
	running  bool
	callback func()
	failures chan error
	inflight sync.WaitGroup
}

// Run 把控件绘制绑定到宿主主线程的 Metal 帧。回调内直接调用 Begin/End 与控件，禁止 NewFrame。
// 一次只允许一个回调；上下文取消、回调 panic 或宿主不可用时释放占用并返回明确错误。
// 这是自身 WindowScene 的 UI；独立进程没有场景时返回不支持，不冒充跨 App 悬浮。
func Run(ctx context.Context, draw func()) error {
	if ctx == nil || draw == nil {
		return types.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	drawState.Lock()
	if drawState.running {
		drawState.Unlock()
		return types.ErrBusy
	}
	drawState.running = true
	drawState.callback = draw
	drawState.failures = make(chan error, 1)
	failures := drawState.failures
	drawState.Unlock()
	defer func() {
		C.agImSetDrawCallback(nil)
		drawState.Lock()
		drawState.callback = nil
		drawState.Unlock()
		drawState.inflight.Wait() // 返回前等待已进入的帧，避免解释器销毁后回调仍在访问它。
		_ = native.Call(context.Background(), "imgui.detach", nil, nil)
		drawState.Lock()
		drawState.running = false
		drawState.Unlock()
	}()
	C.agImInstallGoDraw()
	if err := native.Call(ctx, "imgui.attach", nil, nil); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-failures:
		return err
	}
}

//export agImGoDraw
func agImGoDraw() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	drawState.Lock()
	callback, failures := drawState.callback, drawState.failures
	if callback != nil {
		drawState.inflight.Add(1)
	}
	drawState.Unlock()
	if callback == nil {
		return
	}
	defer drawState.inflight.Done()
	defer func() {
		if value := recover(); value != nil {
			drawState.Lock()
			drawState.callback = nil
			drawState.Unlock()
			select {
			case failures <- fmt.Errorf("ImGui 绘制回调失败: %v", value):
			default:
			}
		}
	}()
	callback()
}
func requireFrame() {
	if C.agImFrameActive() == 0 {
		panic("ImGui 控件必须在 Run 的主线程绘制回调内调用")
	}
}

// Checkbox 返回本帧是否改变，value 的所有权始终在 Go；不会把 Go 指针留在 C++。
func Checkbox(label string, value *bool) bool {
	requireFrame()
	if value == nil {
		panic(types.ErrInvalid)
	}
	s := C.CString(label)
	defer C.free(unsafe.Pointer(s))
	var v C.int
	if *value {
		v = 1
	}
	changed := C.agImCheckbox(s, &v) != 0
	*value = v != 0
	return changed
}
func SliderFloat(label string, value *float32, min, max float32) bool {
	requireFrame()
	if value == nil || !(min <= max) {
		panic(types.ErrInvalid)
	}
	s := C.CString(label)
	defer C.free(unsafe.Pointer(s))
	v := C.float(*value)
	changed := C.agImSliderFloat(s, &v, C.float(min), C.float(max)) != 0
	*value = float32(v)
	return changed
}
func SliderInt(label string, value *int, min, max int) bool {
	requireFrame()
	if value == nil || min > max || min < -2147483648 || max > 2147483647 || *value < -2147483648 || *value > 2147483647 {
		panic(types.ErrInvalid)
	}
	s := C.CString(label)
	defer C.free(unsafe.Pointer(s))
	v := C.int(*value)
	changed := C.agImSliderInt(s, &v, C.int(min), C.int(max)) != 0
	*value = int(v)
	return changed
}

// InputText 使用有界 C 缓冲，容量包含末尾 NUL。手机键盘输入仍取决于宿主输入事件装配。
func InputText(label string, value *string, capacity int) bool {
	requireFrame()
	if value == nil || capacity < 2 || capacity > 1<<20 || len(*value) >= capacity {
		panic(types.ErrInvalid)
	}
	s := C.CString(label)
	defer C.free(unsafe.Pointer(s))
	buffer := C.calloc(C.size_t(capacity), 1)
	if buffer == nil {
		panic("输入缓冲分配失败")
	}
	defer C.free(buffer)
	if len(*value) > 0 {
		data := []byte(*value)
		C.memcpy(buffer, unsafe.Pointer(&data[0]), C.size_t(len(data)))
	}
	changed := C.agImInputText(s, (*C.char)(buffer), C.size_t(capacity)) != 0
	*value = C.GoString((*C.char)(buffer))
	return changed
}
