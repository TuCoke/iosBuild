// Package inputstate 串行化 SDK 高层输入动作，避免文字和手势交错提交。
package inputstate

import (
	"context"

	"autogov1/v2/sdk/ios/types"
)

var gate = make(chan struct{}, 1)
var taskGate = make(chan struct{}, 1)

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
		if err := acquireDevice(ctx); err != nil {
			<-gate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func Release() { releaseDevice(); <-gate }

// BeginTask 在整个解释器执行期间持有设备租约。动作锁可在同一进程内嵌套，
// 但另一进程只能得到忙错误；不让人工操作插入脚本的等待/观察阶段。
func BeginTask(ctx context.Context) (func(), error) {
	if ctx == nil {
		return nil, types.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case taskGate <- struct{}{}:
	default:
		return nil, types.ErrBusy
	}
	if err := acquireDevice(ctx); err != nil {
		<-taskGate
		return nil, err
	}
	return func() { releaseDevice(); <-taskGate }, nil
}
