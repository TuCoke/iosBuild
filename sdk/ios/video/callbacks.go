package video

import (
	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/types"
	"fmt"
)

// H264StreamSetCallback 注册 Annex B 数据回调。每次回调的数据已经复制，可在回调后保留。
// 启动前设置；回调和拉取两种模式共享编码器，同一时间只允许其中一个运行。
func H264StreamSetCallback(fn func([]byte)) error { return native.H264CallbackSet(fn) }
func SetEncoderParams(resolution, bitrate, fps int) error {
	if resolution < 64 || resolution > 4096 || bitrate < 64000 || bitrate > 50000000 || fps < 1 || fps > 60 {
		return fmt.Errorf("%w: 编码参数越界", types.ErrInvalid)
	}
	return native.EncoderParams(resolution, bitrate, fps)
}
func StartEncoding() error { return native.EncoderStart() }

// StopEncoding 等待已开始回调结束；在回调自身调用时不等待自己。
func StopEncoding() error { return native.EncoderStop() }

// RawFrameStart 输出当前显示方向 BGRA 像素，stride 为每行字节数；数据拥有独立 Go 内存。
func RawFrameStart(fn func([]byte, int, int, int)) error {
	if fn == nil {
		return types.ErrInvalid
	}
	if err := native.RawCallbackSet(fn); err != nil {
		return err
	}
	return native.RawStart()
}
func RawFrameStop() error {
	if err := native.RawStop(); err != nil {
		return err
	}
	return native.RawCallbackSet(nil)
}
func CallbackError() error { return native.CallbackError() }
