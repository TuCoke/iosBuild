//go:build ios && cgo

// 全量 API 自检：把每一个原生 op 都真调一次，报告"实现了没有、能不能用"。
//
// 【为什么要做】
// 逐个按钮点只能看到几个功能；用户真正关心的是"所有 API 都能用吗"。
// 这里按 op 列表逐个调，三类结论：
//   ok        —— 调用成功
//   env       —— 已实现，但当前环境拿不到（例：模拟器没有 render server）
//   missing   —— 原生根本没实现（返回"尚未提供此原生能力"）
//
// 【安全边界】
// 会改状态或伤设备的 op（重启、锁屏）不真调，只标 unsafe —— 自检不能把手机弄挂。
package selftest

import (
	"autogov1/v2/sdk/ios/types"
	"context"
	"errors"
	"fmt"
	"time"

	"autogov1/v2/sdk/ios/internal/native"
)

// opProbe 是一条自检项。
type opProbe struct {
	op     string
	args   map[string]any
	unsafe bool // true = 不真调，只标记"未验证"
}

// probeList 覆盖我们实现过的全部原生 op。
//
// 参数刻意用"能立刻失败但不会产生副作用"的值（空串、非法坐标），
// 这样既能验证 op 存在，又不会真的装应用、改剪贴板、重启设备。
func probeList() []opProbe {
	return []opProbe{
		// 运行时
		{"runtime.capabilities", nil, false},
		{"runtime.selftest", map[string]any{"x": 0.5, "y": 0.5}, false},
		// 采集
		{"capture.image", map[string]any{"maxSize": 256, "jpeg": true, "quality": 0.5}, false},
		{"capture.json", map[string]any{"maxSize": 256, "jpeg": true, "quality": 0.5}, false},
		{"capture.release", nil, false},
		// 设备
		{"device.info", nil, false},
		{"device.display", nil, false},
		{"device.battery", nil, false},
		{"device.battery_status", nil, false},
		{"device.screen_on", nil, false},
		{"device.unlocked", nil, false},
		{"device.brightness_get", nil, false},
		{"device.memory", nil, false},
		{"device.ip", nil, false},
		{"device.wifi_mac", nil, false},
		{"device.vpn_status", nil, false},
		{"device.clipget", nil, false},
		{"device.clipset", map[string]any{"text": ""}, false},
		{"device.button", map[string]any{"button": "__probe__"}, false},
		{"device.brightness", map[string]any{"value": -1}, false}, // 越界 → 参数错误
		{"device.reboot", nil, true},                              // 危险，不调
		{"device.lock", nil, true},                                // 危险，不调
		{"device.vpn_set", map[string]any{}, true},
		{"device.wifi_set", map[string]any{}, true},
		{"device.keep_awake", map[string]any{"on": false}, false},
		{"device.wake", nil, false},
		// 应用
		{"app.foreground", nil, false},
		{"app.self", nil, false},
		{"app.list", nil, false},
		{"app.info", map[string]any{"bundleId": "__probe__"}, false},
		{"app.installed", map[string]any{"bundleId": "__probe__"}, false},
		{"app.bundle_path", map[string]any{"bundleId": "__probe__"}, false},
		{"app.data_path", map[string]any{"bundleId": "__probe__"}, false},
		{"app.bundle_dir", nil, false},
		{"app.launch", map[string]any{"bundleId": ""}, false}, // 缺参 → 参数错误
		{"app.stop", map[string]any{"bundleId": ""}, false},   // 缺参
		{"app.open_url", map[string]any{"url": ""}, false},    // 缺参
		{"app.icon", map[string]any{"bundleId": "__probe__"}, false},
		{"app.install", map[string]any{"path": ""}, false},       // 缺参，不会真装
		{"app.uninstall", map[string]any{"bundleId": ""}, false}, // 缺参
		{"app.clear", map[string]any{"bundleId": ""}, false},     // 缺参
		// 输入
		{"motion.frame", map[string]any{"contacts": []any{}}, false},
		{"motion.configure", map[string]any{"senderId": 0}, false},
		{"motion.key", map[string]any{"code": -1}, false}, // 非法键码 → 参数错误
		{"ime.key", map[string]any{"usage": 0}, false},
		{"ime.text", map[string]any{"text": ""}, false},
		// 悬浮层
		{"hud.show", map[string]any{"id": 9901, "x1": 0, "y1": 0, "x2": 10, "y2": 10}, false},
		{"hud.visible", map[string]any{"id": 9901}, false},
		{"hud.hide", map[string]any{"id": 9901}, false},
		{"hud.destroy", map[string]any{"id": 9901}, false},
		{"console.show", map[string]any{"id": 9902, "x": 0, "y": 0, "width": 10, "height": 10}, false},
		{"console.print", map[string]any{"id": 9902, "text": "probe"}, false},
		{"console.clear", map[string]any{"id": 9902}, false},
		{"console.destroy", map[string]any{"id": 9902}, false},
		// 系统 / 工具
		{"system.pid", map[string]any{"name": "__probe__"}, false},
		{"system.memory", map[string]any{"pid": 1}, false},
		{"system.cpu", map[string]any{"pid": 1}, false},
		{"utils.toast", map[string]any{"message": "", "duration": 1}, false},
		// utils.alert 会真的弹 UIAlertController：自检跑完会留一个模态框挡在界面上，
		// 后面的探针、截图全被它盖住（模拟器截图里就出现过空白"确定"弹窗）。
		// 它和 device.reboot 一样"会改状态"，自检不真调。
		{"utils.alert", map[string]any{"title": "", "content": "", "buttons": []any{}}, true},
		{"utils.input_alert", map[string]any{"title": "", "content": "", "placeholder": ""}, true},
		{"utils.alive", map[string]any{"pid": 1}, false},
		{"utils.kill", map[string]any{"pid": -1}, false},
		{"utils.spawn", map[string]any{"path": ""}, false}, // 缺参
		{"utils.exec", map[string]any{"path": ""}, false},  // 缺参
		// 控件树
		{"uiacc.snapshot", map[string]any{"limit": 10}, false},
		// 识别栈（未装库时应报"识别栈未安装"，属于 env）
		{"ppocr.ocr", map[string]any{"base64": ""}, false},
		{"yolo.load", map[string]any{"param": "", "bin": ""}, false},
		{"dotocr.set_dict", map[string]any{"name": "", "dict": ""}, false},
		// 视频
		{"video.start", map[string]any{"fps": 1}, false},
		{"video.stop", nil, false},
		// 外壳通道
	}
}

// SelfTestAll 逐个调 op，返回结构化结果。
func All(ctx context.Context) map[string]any {
	type item struct {
		Op     string `json:"op"`
		Status string `json:"status"` // ok / env / missing / unsafe
		Detail string `json:"detail"`
	}
	var items []item
	counts := map[string]int{"ok": 0, "env": 0, "missing": 0, "unsafe": 0, "invalid": 0, "unverified": 0}

	for _, p := range probeList() {
		if p.unsafe {
			items = append(items, item{Op: p.op, Status: "unsafe", Detail: "会改状态，自检不调"})
			counts["unsafe"]++
			continue
		}
		if err := ctx.Err(); err != nil {
			items = append(items, item{Op: p.op, Status: "unverified", Detail: err.Error()})
			counts["unverified"]++
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := native.Call(callCtx, p.op, p.args, nil)
		cancel()
		switch {
		case err == nil:
			items = append(items, item{Op: p.op, Status: "ok"})
			counts["ok"]++
		case errors.Is(err, types.ErrInvalid):
			items = append(items, item{Op: p.op, Status: "invalid", Detail: "只验证了参数拒绝，未验证能力成功: " + err.Error()})
			counts["invalid"]++
		case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
			items = append(items, item{Op: p.op, Status: "unverified", Detail: err.Error()})
			counts["unverified"]++
		case errors.Is(err, types.ErrUnsupported):
			items = append(items, item{Op: p.op, Status: "missing", Detail: err.Error()})
			counts["missing"]++
		default:
			// 已实现，但这次调用的参数/环境不满足（越界、缺参、无权限、未装库…）
			detail := err.Error()
			if text := []rune(detail); len(text) > 120 {
				detail = string(text[:120])
			}
			items = append(items, item{Op: p.op, Status: "env", Detail: detail})
			counts["env"]++
		}
	}
	return map[string]any{
		"total":   len(items),
		"ok":      counts["ok"],
		"env":     counts["env"],
		"missing": counts["missing"],
		"unsafe":  counts["unsafe"],
		"invalid": counts["invalid"], "unverified": counts["unverified"],
		"items": items,
		"summary": fmt.Sprintf("共 %d 项：调用成功 %d / 环境失败 %d / 不支持 %d / 跳过有副作用 %d / 仅参数拒绝 %d / 未验证 %d",
			len(items), counts["ok"], counts["env"], counts["missing"], counts["unsafe"], counts["invalid"], counts["unverified"]),
	}
}
