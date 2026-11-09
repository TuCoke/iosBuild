// Package videoexample 演示持续读取编码包；传输协议由宿主决定。
package videoexample

import (
	"context"
	"errors"
	"io"

	"autogov1/v2/sdk/ios/video"
)

// CopyAnnexB 将视频写入宿主提供的目标。退出时等待原生流停止，保留关闭错误。
// Writer 自身应支持超时，否则宿主阻塞写入时 context 无法打断该 Writer。
func CopyAnnexB(ctx context.Context, destination io.Writer) (err error) {
	stream, err := video.Start(ctx, video.Config{MaxSize: 960, FPS: 15, Bitrate: 1500000})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, stream.Close()) }()
	for {
		packet, e := stream.Read(ctx)
		if e != nil {
			if errors.Is(e, io.EOF) {
				return ctx.Err()
			}
			return e
		}
		n, e := destination.Write(packet.Data)
		if e != nil {
			return e
		}
		if n != len(packet.Data) {
			return io.ErrShortWrite
		}
	}
}
