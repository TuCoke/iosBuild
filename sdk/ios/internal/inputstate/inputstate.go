// Package inputstate 串行化 SDK 高层输入动作，避免文字和手势交错提交。
package inputstate

import (
	"context"

	"autogov1/v2/sdk/ios/types"
)

var gate = make(chan struct{}, 1)

func Acquire(ctx context.Context) error {
	if ctx == nil {
		return types.ErrInvalid
	}
	select {
	case gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func Release() { <-gate }
