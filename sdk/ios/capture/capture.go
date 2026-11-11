// Package capture 返回独立拥有的截图数据，不暴露会被下一帧覆盖的原生缓冲区。
package capture

import (
	"context"
	"fmt"

	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/types"
)

type Options struct {
	AutoRotation bool    `json:"autoRotation"`
	Rotation     int     `json:"rotation"`
	MaxSize      int     `json:"maxSize"`
	JPEG         bool    `json:"jpeg"`
	Quality      float64 `json:"quality"`
}
type Frame struct {
	Data          []byte
	MIME          string
	Width, Height int
}

// Release 释放进程内复用的采集 surface；已返回的截图不受影响，下次采集会重新创建。
func Release(ctx context.Context) error { return native.Call(ctx, "capture.release", nil, nil) }

func Screenshot(ctx context.Context, o Options) (Frame, error) {
	if o.Rotation < 0 || o.Rotation > 3 || o.MaxSize < 0 || o.MaxSize > 8192 {
		return Frame{}, fmt.Errorf("%w: 截图方向或尺寸无效", types.ErrInvalid)
	}
	if o.Quality == 0 {
		o.Quality = .85
	}
	if !(o.Quality > 0 && o.Quality <= 1) {
		return Frame{}, types.ErrInvalid
	}
	r, err := native.Raw(ctx, "capture.image", o)
	if err != nil {
		return Frame{}, err
	}
	mime := "image/png"
	if o.JPEG {
		mime = "image/jpeg"
	}
	return Frame{Data: r.Data, MIME: mime, Width: r.Width, Height: r.Height}, nil
}
