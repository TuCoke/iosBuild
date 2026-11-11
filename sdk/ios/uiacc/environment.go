package uiacc

import (
	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/types"
	"context"
	"time"
)

// Environment 只说明 AX 引导环境，Ready/Configured 均不代替跨 App 采集验证。
type Environment struct {
	Ready           bool   `json:"ready"`
	Configured      bool   `json:"configured"`
	Mode            string `json:"mode"`
	UID             int    `json:"uid"`
	MobileIdentity  bool   `json:"mobileIdentity"`
	PreviousKnown   bool   `json:"previousKnown"`
	PreviousEnabled bool   `json:"previousEnabled"`
}

// Prepare 引导 AX 服务及辅助功能设置。默认宿主以 mobile 身份运行，不修改整个 Go 进程身份。
func Prepare(ctx context.Context) (Environment, error) {
	var out Environment
	err := native.Call(ctx, "uiacc.prepare", nil, &out)
	return out, err
}

// RestoreEnvironment 在停止所有选择器后显式恢复首次引导前的设置。
func RestoreEnvironment(ctx context.Context) error {
	return native.Call(ctx, "uiacc.restore", nil, nil)
}

type WarmupResult struct {
	ApplicationFound bool `json:"applicationFound"`
	Attempts         int  `json:"attempts"`
	Restored         bool `json:"restored"`
	MobileIdentity   bool `json:"mobileIdentity"`
}

// Warmup 按样本先激活辅助环境，再从屏幕中心预热 AX，最后可恢复首次设置。
// 查询仍统一走 UIKit 主线程；不会在并发 Go 进程内修改 uid/gid。
// restore=true 时保留已经预热的引导状态，不在下一次采集时立即重新开启设置。
func Warmup(ctx context.Context, timeout time.Duration, restore bool) (WarmupResult, error) {
	var out WarmupResult
	if timeout < 50*time.Millisecond || timeout > 2500*time.Millisecond {
		return out, types.ErrInvalid
	}
	err := native.Call(ctx, "uiacc.warmup", map[string]any{"timeoutMs": timeout.Milliseconds(), "restore": restore}, &out)
	return out, err
}
