//go:build ios && cgo

package native

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Foundation -framework UIKit -framework CoreFoundation -framework CoreGraphics -framework IOSurface -framework ImageIO -framework VideoToolbox -framework CoreMedia -framework CoreVideo
#include "bridge.h"
#include <stdlib.h>
*/
import "C"

import "unsafe"

func invoke(op string, input []byte) (Result, error) {
	name := C.CString(op)
	defer C.free(unsafe.Pointer(name))
	arg := C.CBytes(input)
	defer C.free(arg)
	r := C.ag_ios_call(name, (*C.uint8_t)(arg), C.size_t(len(input)))
	defer C.ag_ios_free(r.data)
	// 单个包限制为 64 MiB，既避免 int 截断，也约束原生返回的分配规模。
	if r.length > 64<<20 {
		return Result{}, statusError(op, 3, "原生响应超过 64 MiB")
	}
	data := C.GoBytes(unsafe.Pointer(r.data), C.int(r.length))
	if r.code != 0 {
		return Result{}, statusError(op, int(r.code), string(data))
	}
	return Result{Data: data, PTS: int64(r.pts_us), Keyframe: r.keyframe != 0, Width: int(r.width), Height: int(r.height)}, nil
}
