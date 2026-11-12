//go:build ios && cgo

package main

import (
	"autogov1/v2/agent/ios/app"
	"autogov1/v2/agent/ios/commands"
	"autogov1/v2/agent/ios/runtime"
	"autogov1/v2/sdk/ios/device"
	"autogov1/v2/sdk/ios/types"
	"context"
	"fmt"
	"path/filepath"
)

var iosAgent app.Manager

func startIOSAgent(args map[string]any) error {
	paths, err := device.Paths(context.Background())
	if err != nil {
		return err
	}
	private := filepath.Join(paths.Documents, "Agent")
	// 仅阻止与宿主调试动作同时执行；控制权仍由服务端 Session 和本机 IOHID 闸门统一保障。
	native := &commands.Native{Before: func(ctx context.Context) (func(), error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !hostCallMu.TryLock() {
			return nil, fmt.Errorf("%w: 宿主正在执行调试操作", types.ErrBusy)
		}
		return hostCallMu.Unlock, nil
	}}
	config := runtime.Config{Server: str(args, "server"), Token: str(args, "token"), DataDir: private, AllowInsecure: boolean(args, "allowInsecure")}
	if err = runtime.ValidateConfig(config); err != nil {
		return err
	}
	// Start 只启动后台协程，不能继承一次 C ABI/HTTP 请求的短生命周期。
	if err = iosAgent.Start(context.Background(), commands.New(native), config); err != nil {
		return err
	}
	backgroundRequest("acquire", "ios-agent", false)
	return nil
}
func agentBusyForUpdate() error {
	state := iosAgent.Status()
	if state.State == "cleanup_failed" {
		return fmt.Errorf("iOS Agent 资源清理未确认，请核对现场并重启宿主，不能直接替换 SDK")
	}
	if state.Running {
		return fmt.Errorf("iOS Agent 正在运行，请先停止并确认清理完成后再更新 SDK")
	}
	return nil
}
