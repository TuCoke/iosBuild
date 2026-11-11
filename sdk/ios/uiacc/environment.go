package uiacc

import (
	"autogov1/v2/sdk/ios/internal/native"
	"context"
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
