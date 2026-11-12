package commands

import (
	"autogov1/v2/protocol"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// 只声明真正实现的命令，安卓专有能力不会在 iOS 上被静默接受。
var commands = []string{"prepare_task", "touch", "tap", "swipe", "longpress", "long_press", "wake", "sleep", "text", "key", "open_app", "open_url", "current_app", "device_info", "ui_dump", "observe", "app_list", "get_clipboard", "set_clipboard", "set_brightness", "screen_on", "screen_off", "ime_on", "ime_off", "input_info", "screen_state", "app_info", "app_stop", "pinch", "gesture", "input_reset", "set_screen", "ui_click", "ui_wait", "cpu_usage"}

func (c *Driver) Execute(ctx context.Context, cmd protocol.Cmd) (any, error) {
	method := ""
	args := map[string]any{}
	switch cmd.Cmd {
	case "prepare_task":
		return c.prepareTask(ctx)
	case "touch":
		if len(cmd.Frames) > 0 {
			return c.Execute(ctx, protocol.Cmd{Cmd: "gesture", Frames: cmd.Frames})
		}
		phase := "move"
		if cmd.Action == 0 {
			phase = "down"
		}
		if cmd.Action == 1 {
			phase = "up"
		}
		info, e := c.Info(ctx)
		if e != nil {
			return nil, e
		}
		w, h := info.Display.Width, info.Display.Height
		if info.Display.Rotation%2 != 0 {
			w, h = h, w
		}
		method = "motion.pointer"
		args = map[string]any{"x": cmd.X * float64(w), "y": cmd.Y * float64(h), "id": 0, "phase": phase}
	case "tap":
		method = "motion.tap"
		args = map[string]any{"x": cmd.X, "y": cmd.Y}
	case "swipe":
		method = "motion.swipe"
		d := cmd.Dur
		if d <= 0 {
			d = 400
		}
		args = map[string]any{"x1": cmd.X, "y1": cmd.Y, "x2": cmd.X2, "y2": cmd.Y2, "durationMs": d}
	case "longpress", "long_press":
		method = "motion.longpress"
		d := cmd.Dur
		if d <= 0 {
			d = 600
		}
		args = map[string]any{"x": cmd.X, "y": cmd.Y, "durationMs": d}
	case "text":
		method = "ime.text"
		args["text"] = cmd.Text
	case "key":
		if cmd.Key == 4 {
			return nil, fmt.Errorf("iOS 没有 Android 全局返回键；请点击页面返回按钮或使用应用支持的返回手势")
		}
		method = "motion.key"
		args["code"] = cmd.Key
	case "open_app":
		method = "app.launch"
		args["bundleId"] = cmd.Pkg
	case "open_url":
		method = "app.open_url"
		args["url"] = cmd.Val
	case "current_app":
		method = "app.foreground"
	case "screen_state":
		method = "device.screen_on"
	case "set_screen":
		method = "device.lock"
		if cmd.On {
			method = "device.wake"
		}
	case "app_info":
		method = "app.info"
		args["bundleId"] = cmd.Pkg
	case "app_stop":
		method = "app.stop"
		args["bundleId"] = cmd.Pkg
	case "cpu_usage":
		method = "system.cpu"
		args["pid"] = cmd.Value
	case "input_reset":
		method = "motion.frame"
		args["contacts"] = []any{}
	case "pinch":
		duration := cmd.Dur
		if duration <= 0 {
			duration = 400
		}
		point := func(x, y float64) map[string]any { return map[string]any{"x": x, "y": y} }
		method = "motion.pinch"
		args = map[string]any{"a0": point(cmd.X-cmd.X2, cmd.Y), "b0": point(cmd.X+cmd.X2, cmd.Y), "a1": point(cmd.X-cmd.Y2, cmd.Y), "b1": point(cmd.X+cmd.Y2, cmd.Y), "durationMs": duration}
	case "gesture":
		for _, frame := range cmd.Frames {
			for _, p := range frame.Contacts {
				if p.Pressure != nil || p.TouchMajor != nil || p.TouchMinor != nil || p.WidthMajor != nil || p.WidthMinor != nil || p.Orientation != nil {
					return nil, fmt.Errorf("iOS Agent 轨迹仅支持触点位置，不能静默忽略压力或接触面积参数")
				}
			}
		}
		if len(cmd.Frames) == 0 {
			return nil, fmt.Errorf("轨迹不能为空")
		}
		method = "motion.gesture"
		args["frames"] = cmd.Frames
	case "ui_click", "ui_wait":
		method = "agent.ui"
		args = map[string]any{"action": cmd.Cmd, "by": cmd.By, "value": cmd.Val, "timeoutMs": cmd.Value}
	case "device_info":
		return c.Info(ctx)
	case "ui_dump":
		method = "uiacc.snapshot"
	case "app_list":
		var apps []struct {
			BundleID string `json:"bundleId"`
			Name     string `json:"name"`
			System   bool   `json:"system"`
		}
		if err := c.Invoke(ctx, "app.list_all", map[string]any{"system": true}, &apps); err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(apps))
		filter := strings.ToLower(strings.TrimSpace(cmd.Val))
		for _, app := range apps {
			if filter != "" && !strings.Contains(strings.ToLower(app.Name), filter) && !strings.Contains(strings.ToLower(app.BundleID), filter) {
				continue
			}
			if app.BundleID != "" {
				out = append(out, map[string]any{"pkg": app.BundleID, "label": app.Name, "system": app.System})
			}
		}
		return out, nil
	case "observe":
		nodes, err := c.Tree(ctx)
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for _, n := range nodes {
			text, _ := n["text"].(string)
			if text == "" {
				text, _ = n["desc"].(string)
			}
			if strings.TrimSpace(text) == "" {
				continue
			}
			x, xok := n["cx"].(float64)
			y, yok := n["cy"].(float64)
			if !xok || !yok || x < 0 || x > 1 || y < 0 || y > 1 {
				continue
			}
			out = append(out, map[string]any{"i": len(out) + 1, "t": text, "x": n["cx"], "y": n["cy"], "k": n["class"]})
		}
		return out, nil
	case "get_clipboard":
		method = "device.clipget"
	case "set_clipboard":
		method = "device.clipset"
		args["text"] = cmd.Text
	case "set_brightness":
		method = "device.brightness"
		args["value"] = float64(cmd.Value) / 255
	case "screen_on", "wake":
		method = "device.wake"
	case "screen_off", "sleep":
		method = "device.lock"
	case "ime_on", "ime_off":
		return map[string]any{"required": false, "platform": "ios", "input": "native-unicode"}, nil
	case "input_info":
		return map[string]any{"platform": "ios", "input": "native-unicode"}, nil
	default:
		return nil, fmt.Errorf("iOS Agent 未实现命令 %s", cmd.Cmd)
	}
	var out json.RawMessage
	if err := c.Invoke(ctx, method, args, &out); err != nil {
		return nil, err
	}
	if cmd.Cmd == "open_app" {
		// 启动只提交一次；在当前会话内轮询前台，核对失败不代表存在待重放的业务写入。
		check, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		foreground := "未读取到"
		var lastError error
		for {
			var fg string
			lastError = c.Invoke(check, "app.foreground", map[string]any{}, &fg)
			if lastError == nil {
				foreground = fg
				if fg == cmd.Pkg {
					return map[string]any{"foreground": fg}, nil
				}
			}
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-check.Done():
				timer.Stop()
				return nil, fmt.Errorf("应用启动未达标: 目标=%s，实际前台=%s，最后查询错误=%v", cmd.Pkg, foreground, lastError)
			case <-timer.C:
			}
		}
	}
	return out, nil
}
func supports(name string) bool {
	for _, n := range commands {
		if n == name {
			return true
		}
	}
	return false
}
func readOnly(cmd protocol.Cmd) bool {
	return protocol.IsObservation(cmd) || cmd.Cmd == "ime_on" || cmd.Cmd == "ime_off"
}
func message(err error) string {
	if err == nil {
		return "iOS 调用已返回；业务结果仍需核对"
	}
	s := err.Error()
	if len(s) > 1200 {
		s = s[:1200]
	}
	return strings.ReplaceAll(s, "\n", " ")
}

// prepareTask 在模型看图前完成确定性的亮屏、无密码锁屏退出与回首页。
// 密码锁仍由用户解锁；绝不把接口受理当作屏幕已准备成功。
func (c *Driver) prepareTask(ctx context.Context) (any, error) {
	if err := c.Invoke(ctx, "device.wake", map[string]any{}, nil); err != nil {
		return nil, err
	}
	unlocked := func() (bool, error) {
		var state struct {
			Unlocked json.RawMessage `json:"unlocked"`
		}
		if err := c.Invoke(ctx, "device.unlocked", map[string]any{}, &state); err != nil {
			return false, err
		}
		value := string(state.Unlocked)
		if value != "true" && value != "false" && value != "1" && value != "0" {
			return false, fmt.Errorf("无法确认设备锁屏状态，任务未开始")
		}
		return value == "true" || value == "1", nil
	}
	ready, err := unlocked()
	if err != nil {
		return nil, err
	}
	if !ready {
		// 只尝试一次普通上滑，适用于无密码/已完成生物识别的锁屏，不输入密码。
		if err = c.Invoke(ctx, "motion.swipe", map[string]any{"x1": 0.5, "y1": 0.92, "x2": 0.5, "y2": 0.25, "durationMs": 350}, nil); err != nil {
			return nil, err
		}
		for i := 0; i < 8; i++ {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
			ready, err = unlocked()
			if err != nil {
				return nil, err
			}
			if ready {
				break
			}
		}
		if !ready {
			return nil, fmt.Errorf("等待解锁：屏幕已唤醒，但设备仍锁定，请手动解锁后重新执行任务")
		}
	}
	if err = c.Invoke(ctx, "motion.key", map[string]any{"code": 3}, nil); err != nil {
		return nil, err
	}
	for i := 0; i < 10; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		var fg string
		if err = c.Invoke(ctx, "app.foreground", map[string]any{}, &fg); err != nil {
			return nil, err
		}
		if fg == "com.apple.springboard" {
			return map[string]any{"unlocked": true, "home": true}, nil
		}
	}
	return nil, fmt.Errorf("主页键已提交，但未确认回到首页，任务未开始")
}
