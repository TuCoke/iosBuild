// Package basic 演示由已有 iOS 宿主调用 SDK，不负责启动 UIKit 或连接云端。
package basic

import (
	"context"
	"fmt"

	"autogov1/v2/sdk/ios/app"
	"autogov1/v2/sdk/ios/capture"
	"autogov1/v2/sdk/ios/device"
	"autogov1/v2/sdk/ios/ime"
	"autogov1/v2/sdk/ios/motion"
)

// Run 启动调用者指定的应用、点击已知输入框、输入文字并返回截图。
// SDK 返回表示请求提交或图像取得，不把这些步骤等同于业务任务已完成。
func Run(ctx context.Context, bundleID, text string, x, y float64, ready func(context.Context) error) (capture.Frame, error) {
	if ready == nil {
		return capture.Frame{}, fmt.Errorf("必须由宿主提供页面就绪检查，不能启动应用后立即盲点")
	}
	info, err := device.Info(ctx)
	if err != nil {
		return capture.Frame{}, err
	}
	if info.Display.Width <= 0 || info.Display.Height <= 0 {
		return capture.Frame{}, fmt.Errorf("屏幕尺寸无效")
	}
	if err = motion.Configure(ctx, 0, 0); err != nil {
		return capture.Frame{}, err
	}
	if err = app.LaunchBundle(ctx, bundleID); err != nil {
		return capture.Frame{}, err
	}
	// 由宿主根据截图或控件确认页面已准备好，不用固定 sleep 冒充业务就绪。
	if err = ready(ctx); err != nil {
		return capture.Frame{}, err
	}
	if err = motion.Tap(ctx, x, y); err != nil {
		return capture.Frame{}, err
	}
	if err = ime.InputTextContext(ctx, text); err != nil {
		return capture.Frame{}, err
	}
	return capture.Screenshot(ctx, capture.Options{JPEG: true, MaxSize: 960})
}
