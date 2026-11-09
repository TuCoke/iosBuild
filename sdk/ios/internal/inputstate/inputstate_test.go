package inputstate

import (
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
