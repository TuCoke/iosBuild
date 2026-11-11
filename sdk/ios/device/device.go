// Package device 提供 iOS 设备信息和系统按键，不包含连接或租户管理。
package device

import (
	"context"
	"fmt"

	"autogov1/v2/sdk/ios/internal/inputstate"
	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/types"
)

func Info(ctx context.Context) (types.DeviceInfo, error) {
	var v types.DeviceInfo
	err := native.Call(ctx, "device.info", nil, &v)
	return v, err
}
func Display(ctx context.Context) (types.Display, error) {
	var v types.Display
	err := native.Call(ctx, "device.display", nil, &v)
	return v, err
}
func Capabilities(ctx context.Context) (types.Capabilities, error) {
	var v types.Capabilities
	err := native.Call(ctx, "runtime.capabilities", nil, &v)
	return v, err
}
func Clipboard(ctx context.Context) (string, error) {
	var v struct {
		Text string `json:"text"`
	}
	err := native.Call(ctx, "device.clipget", nil, &v)
	return v.Text, err
}
func SetClipboard(ctx context.Context, text string) error {
	return native.Call(ctx, "device.clipset", map[string]any{"text": text}, nil)
}

type Button string

const (
	Home       Button = "home"
	Power      Button = "power"
	VolumeUp   Button = "volume_up"
	VolumeDown Button = "volume_down"
)

func Press(ctx context.Context, b Button) error {
	switch b {
	case Home, Power, VolumeUp, VolumeDown:
	default:
		return fmt.Errorf("%w: 未知系统按键 %q", types.ErrInvalid, b)
	}
	if err := inputstate.Acquire(ctx); err != nil {
		return err
	}
	defer inputstate.Release()
	return native.Call(ctx, "device.button", map[string]any{"button": b}, nil)
}

// Lock 只请求锁屏，不提供绕过系统密码的解锁接口。
func Lock(ctx context.Context) error { return native.Call(ctx, "device.lock", nil, nil) }
func SetBrightness(ctx context.Context, value float64) error {
	if !(value >= 0 && value <= 1) {
		return fmt.Errorf("%w: 亮度应为 0..1", types.ErrInvalid)
	}
	return native.Call(ctx, "device.brightness", map[string]any{"value": value}, nil)
}

// RuntimePaths 由原生宿主解析。更新目录不依赖脚本工作目录，loadedImage 来自 dladdr。
type RuntimePaths struct {
	Platform    string `json:"platform"`
	Documents   string `json:"documents"`
	Bundle      string `json:"bundle"`
	LoadedImage string `json:"loadedImage"`
	Executable  string `json:"executable"`
}

func Paths(ctx context.Context) (RuntimePaths, error) {
	var paths RuntimePaths
	err := native.Call(ctx, "runtime.paths", nil, &paths)
	return paths, err
}
