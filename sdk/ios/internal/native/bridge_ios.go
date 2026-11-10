//go:build ios && cgo

package native

/*
#cgo CFLAGS: -fblocks
// SRCDIR 由 cgo 展开成绝对路径；OpenCV 头写在这里才不依赖 CI 的工作目录
// （之前放在 CI 的 CGO_CXXFLAGS 里是相对路径，cgo 在包目录下调用编译器时失效）。
// 没有 opencv2.framework 时，ag_rec.mm 的 __has_include 会走降级分支（识别栈报不可用）。
#cgo CXXFLAGS: -I${SRCDIR}/../../../../host/ios/Frameworks/opencv2.framework/Headers
// 不链 -framework IOSurface：iOS 上它是私有框架，且代码已改为 dlsym 解析符号。
#cgo LDFLAGS: -framework Foundation -framework UIKit -framework CoreFoundation -framework CoreGraphics -framework ImageIO -framework VideoToolbox -framework CoreMedia -framework CoreVideo
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
	// ag_ios_free 的参数是 void*：cgo 生成的包装要求显式 unsafe.Pointer，
	// 直接传 *C.uint8_t 会在 ios+cgo 构建时报
	//   cannot use _cgo0 (variable of type *_Ctype_uint8_t) as unsafe.Pointer value
	// （这条路径在 Windows 上是 build tag 排除的，只有真 iOS 构建才编译到。）
	defer C.ag_ios_free(unsafe.Pointer(r.data))
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
