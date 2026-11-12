package inputstate

import (
	"autogov1/v2/sdk/ios/types"
	"context"
	"errors"
	"testing"
)

func TestCanceledWaitDoesNotStealOrLeakInputGate(t *testing.T) {
	if err := Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Acquire(ctx); !errors.Is(err, context.Canceled) {
		Release()
		t.Fatalf("等待者取消未生效: %v", err)
	}
	Release()
	// 闸门空闲时取消与发送都可被 select 选中，也必须回收已占的令牌。
	for i := 0; i < 20; i++ {
		if err := Acquire(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("已取消调用占用了输入闸门: %v", err)
		}
	}
	if err := Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	Release()
}

func TestTaskLeaseLifecycleAndNestedAction(t *testing.T) {
	release, err := BeginTask(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = BeginTask(context.Background()); !errors.Is(err, types.ErrBusy) {
		release()
		t.Fatalf("同一进程的第二个设备任务没有被拒绝: %v", err)
	}
	if err = Acquire(context.Background()); err != nil {
		release()
		t.Fatal(err)
	}
	Release() // 任务里的高层动作可以嵌套取得自己的引用。
	release()
	release, err = BeginTask(context.Background())
	if err != nil {
		t.Fatal("任务结束后没有释放所有权", err)
	}
	release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = BeginTask(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
