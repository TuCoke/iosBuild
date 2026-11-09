//go:build !ios || !cgo

package device_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"autogov1/v2/sdk/ios/app"
	"autogov1/v2/sdk/ios/capture"
	"autogov1/v2/sdk/ios/device"
	"autogov1/v2/sdk/ios/ime"
	"autogov1/v2/sdk/ios/motion"
	"autogov1/v2/sdk/ios/types"
	"autogov1/v2/sdk/ios/video"
)

func TestUnsupportedPlatformNeverReportsDeviceSuccess(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func() error{
		"设备信息": func() error { _, err := device.Info(ctx); return err },
		"能力探测": func() error { _, err := device.Capabilities(ctx); return err },
		"应用启动": func() error { return app.Launch(ctx, "com.example.demo") },
		"点击":   func() error { return motion.Tap(ctx, .5, .5) },
		"滑动":   func() error { return motion.Swipe(ctx, .5, .8, .5, .2, time.Millisecond) },
		"文本":   func() error { return ime.InputText(ctx, "杭州") },
		"截图":   func() error { _, err := capture.Screenshot(ctx, capture.Options{}); return err },
		"视频":   func() error { _, err := video.Start(ctx, video.Config{}); return err },
	}
	for name, invoke := range cases {
		t.Run(name, func(t *testing.T) {
			if err := invoke(); !errors.Is(err, types.ErrUnsupported) {
				t.Fatalf("应明确返回不支持，而不是成功: %v", err)
			}
		})
	}
}

func TestInvalidRequestsFailBeforeNativeDispatch(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func() error{
		"越界点击": func() error { return motion.Tap(ctx, 1.1, .5) },
		"无效时长": func() error { return motion.Swipe(ctx, .5, .8, .5, .2, 0) },
		"重复手指": func() error {
			return motion.TouchFrame(ctx, []types.Contact{{ID: 0, Point: types.Point{X: .1, Y: .1}}, {ID: 0, Point: types.Point{X: .2, Y: .2}}})
		},
		"倒序轨迹":   func() error { return motion.Gesture(ctx, []types.Frame{{AtMS: 0}, {AtMS: 0}}) },
		"无效编码配置": func() error { _, err := video.Start(ctx, video.Config{FPS: 61}); return err },
		"无效截图方向": func() error { _, err := capture.Screenshot(ctx, capture.Options{Rotation: 4}); return err },
	}
	for name, invoke := range cases {
		t.Run(name, func(t *testing.T) {
			if err := invoke(); !errors.Is(err, types.ErrInvalid) {
				t.Fatalf("参数未在调用设备前被拒绝: %v", err)
			}
		})
	}
}

func TestCanceledCommandPreservesNoExecutionResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := motion.Tap(ctx, .5, .5); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消不应变成设备操作失败或未知: %v", err)
	}
	if _, err := device.Info(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消后仍调用了原生接口: %v", err)
	}
}
