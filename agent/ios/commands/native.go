package commands

import (
	"autogov1/v2/agent/ios/runtime"
	"autogov1/v2/sdk/ios/app"
	"autogov1/v2/sdk/ios/capture"
	"autogov1/v2/sdk/ios/device"
	"autogov1/v2/sdk/ios/ime"
	"autogov1/v2/sdk/ios/motion"
	sdkbridge "autogov1/v2/sdk/ios/runtime"
	"autogov1/v2/sdk/ios/types"
	"autogov1/v2/sdk/ios/uiacc"
	"autogov1/v2/sdk/ios/video"
	"context"
	"encoding/json"

	"fmt"
	"time"
)

// Native 直接使用 iOS SDK；Before 只用于宿主本进程的调试/升级互斥，不改变云端 Session。
type Native struct {
	Before func(context.Context) (func(), error)
}

func decode(v any, out any) error {
	if out == nil {
		return nil
	}
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, out)
}
func (n *Native) guard(ctx context.Context) (func(), error) {
	if n.Before != nil {
		return n.Before(ctx)
	}
	return func() {}, nil
}
func (n *Native) Info(ctx context.Context) (runtime.Info, error) {
	release, e := n.guard(ctx)
	if e != nil {
		return runtime.Info{}, e
	}
	defer release()
	v, e := device.Info(ctx)
	var out runtime.Info
	if e == nil {
		e = decode(v, &out)
	}
	return out, e
}
func (n *Native) Healthy(ctx context.Context) error {
	v, e := device.Info(ctx)
	if e == nil && (v.OS != "ios" || v.Identifier == "") {
		return fmt.Errorf("iOS 运行身份不可用")
	}
	return e
}
func (n *Native) Screenshot(ctx context.Context) ([]byte, error) {
	release, e := n.guard(ctx)
	if e != nil {
		return nil, e
	}
	defer release()
	v, e := capture.Screenshot(ctx, capture.Options{AutoRotation: true, JPEG: true, MaxSize: 1024, Quality: .75})
	return v.Data, e
}
func (n *Native) Invoke(ctx context.Context, method string, args any, out any) error {
	release, e := n.guard(ctx)
	if e != nil {
		return e
	}
	defer release()
	b, e := json.Marshal(args)
	if e != nil {
		return e
	}
	a := map[string]any{}
	if e = json.Unmarshal(b, &a); e != nil {
		return e
	}
	number := func(k string, d float64) float64 {
		if v, ok := a[k].(float64); ok {
			return v
		}
		return d
	}
	text := func(k string) string { v, _ := a[k].(string); return v }
	value := any(map[string]any{"submitted": true})
	switch method {
	case "motion.tap":
		e = motion.Tap(ctx, number("x", -1), number("y", -1))
	case "motion.longpress":
		e = motion.LongPress(ctx, number("x", -1), number("y", -1), time.Duration(number("durationMs", 600))*time.Millisecond)
	case "motion.swipe":
		e = motion.SwipePath(ctx, number("x1", -1), number("y1", -1), number("x2", -1), number("y2", -1), time.Duration(number("durationMs", 400))*time.Millisecond)
	case "motion.gesture":
		var frames []types.Frame
		raw, _ := json.Marshal(a["frames"])
		if e = json.Unmarshal(raw, &frames); e == nil {
			e = motion.Gesture(ctx, frames)
		}
	case "motion.pinch":
		var points [4]types.Point
		for i, k := range []string{"a0", "b0", "a1", "b1"} {
			raw, _ := json.Marshal(a[k])
			if e = json.Unmarshal(raw, &points[i]); e != nil {
				return e
			}
			if !points[i].Valid() {
				return types.ErrInvalid
			}
		}
		e = motion.Pinch(ctx, points[0], points[1], points[2], points[3], time.Duration(number("durationMs", 400))*time.Millisecond)
	case "app.launch":
		e = app.LaunchBundle(ctx, text("bundleId"))
	case "app.foreground":
		value, e = app.Foreground(ctx)
	case "ime.text":
		e = ime.InputTextContext(ctx, text("text"))
	case "agent.ui":
		u := uiacc.NewWithContext(ctx)
		defer u.Release()
		switch text("by") {
		case "text":
			u.Text(text("value"))
		case "text_contains":
			u.TextContains(text("value"))
		case "id":
			u.Id(text("value"))
		case "desc":
			u.Desc(text("value"))
		case "class":
			u.ClassName(text("value"))
		default:
			return fmt.Errorf("iOS 不支持该控件选择器")
		}
		duration := time.Duration(number("timeoutMs", 5000)) * time.Millisecond
		if duration <= 0 || duration > 120*time.Second {
			duration = 5 * time.Second
		}
		wait, cancel := context.WithTimeout(ctx, duration)
		defer cancel()
		node, findErr := u.WaitForContext(wait)
		if findErr != nil {
			return findErr
		}
		if node == nil {
			return fmt.Errorf("未找到目标控件")
		}
		if text("action") == "ui_click" {
			e = node.ClickContext(ctx)
		} else {
			value = map[string]any{"found": true, "text": node.GetText(), "id": node.GetId()}
		}
	default:
		// 复用 SDK 已有的结构化原生边界，仅传固定命令映射；不启动解释器、不接受远程任意代码。
		raw, err := sdkbridge.CallJSON(ctx, method, a)
		if err != nil {
			return err
		}
		if out != nil {
			return json.Unmarshal(raw, out)
		}
		return nil
	}
	if e != nil {
		return e
	}
	return decode(value, out)
}

type nativeVideo struct{ *video.Stream }

func (v *nativeVideo) Read(ctx context.Context) (runtime.VideoPacket, error) {
	p, e := v.Stream.Read(ctx)
	return runtime.VideoPacket{Data: p.Data, PTS: p.PTS.Microseconds(), Keyframe: p.Keyframe, Width: p.Width, Height: p.Height}, e
}
func (n *Native) OpenVideo(ctx context.Context, c runtime.VideoOptions) (runtime.VideoSource, error) {
	v, e := video.Start(ctx, video.Config{AutoRotation: true, MaxSize: c.Width, FPS: c.FPS, Bitrate: c.Bitrate})
	if e != nil {
		return nil, e
	}
	return &nativeVideo{v}, nil
}
