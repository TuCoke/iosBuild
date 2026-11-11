//go:build ios && cgo

package native

/*
#include <stdint.h>
#include <stdlib.h>
#include "ag_bridge.h"
extern void agov_go_h264(void *, int);
extern void agov_go_raw(void *, int, int, int);
*/
import "C"
import (
	"autogov1/v2/sdk/ios/types"
	"fmt"
	"sync"
	"unsafe"
)

var callbackMu sync.RWMutex
var h264Handler func([]byte)
var rawHandler func([]byte, int, int, int)
var callbackFailure error

func H264CallbackSet(fn func([]byte)) error {
	callbackMu.Lock()
	defer callbackMu.Unlock()
	var ptr unsafe.Pointer
	if fn != nil {
		ptr = unsafe.Pointer(C.agov_go_h264)
	}
	if C.BridgeH264SetCallback(ptr) != 0 {
		return types.ErrBusy
	}
	h264Handler = fn
	callbackFailure = nil
	return nil
}
func RawCallbackSet(fn func([]byte, int, int, int)) error {
	callbackMu.Lock()
	defer callbackMu.Unlock()
	var ptr unsafe.Pointer
	if fn != nil {
		ptr = unsafe.Pointer(C.agov_go_raw)
	}
	if C.BridgeRawFrameSetCallback(ptr) != 0 {
		return types.ErrBusy
	}
	rawHandler = fn
	callbackFailure = nil
	return nil
}
func CallbackError() error {
	callbackMu.RLock()
	err := callbackFailure
	callbackMu.RUnlock()
	if err != nil {
		return err
	}
	message := C.agov_stream_error()
	if message == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(message))
	return fmt.Errorf("%s", C.GoString(message))
}
func callbackResult(code C.int) error {
	if code != 0 {
		return types.ErrUnavailable
	}
	return nil
}
func EncoderParams(resolution, bitrate, fps int) error {
	return callbackResult(C.BridgeH264SetEncoderParams(C.int(resolution), C.int(bitrate), C.int(fps)))
}
func EncoderStart() error { return callbackResult(C.BridgeH264StartEncoding()) }
func EncoderStop() error  { return callbackResult(C.BridgeH264StopEncoding()) }
func RawStart() error     { return callbackResult(C.BridgeRawFrameStart()) }
func RawStop() error      { return callbackResult(C.BridgeRawFrameStop()) }
func callbackPanic(raw bool) {
	if v := recover(); v != nil {
		callbackMu.Lock()
		callbackFailure = fmt.Errorf("视频回调异常: %v", v)
		callbackMu.Unlock()
		if raw {
			C.BridgeRawFrameStop()
		} else {
			C.BridgeH264StopEncoding()
		}
	}
}

//export agov_go_h264
func agov_go_h264(data unsafe.Pointer, size C.int) {
	defer callbackPanic(false)
	if data == nil || size <= 0 || size > 64<<20 {
		return
	}
	callbackMu.RLock()
	fn := h264Handler
	callbackMu.RUnlock()
	if fn != nil {
		fn(C.GoBytes(data, size))
	}
}

//export agov_go_raw
func agov_go_raw(data unsafe.Pointer, w, h, stride C.int) {
	defer callbackPanic(true)
	size := int64(h) * int64(stride)
	if data == nil || w <= 0 || h <= 0 || int64(stride) < int64(w)*4 || size <= 0 || size > 64<<20 {
		return
	}
	callbackMu.RLock()
	fn := rawHandler
	callbackMu.RUnlock()
	if fn != nil {
		fn(C.GoBytes(data, C.int(size)), int(w), int(h), int(stride))
	}
}
