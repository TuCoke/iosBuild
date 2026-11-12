//go:build ios && cgo

package inputstate

import (
	"autogov1/v2/sdk/ios/internal/native"
	"context"
)

func acquireDevice(ctx context.Context) error {
	return native.Call(ctx, "runtime.input.acquire", nil, nil)
}
func releaseDevice() {
	// 取消不能阻止释放本进程已经取得的租约。
	_ = native.Call(context.Background(), "runtime.input.release", nil, nil)
}
