package script

// DemoSource 是内置示例脚本。
//
// 它同时充当"解释器是否装好"的验收用例：跑通就说明
//   · yaegi 能装载我们的符号表
//   · 脚本能通过 api.Call 打到原生能力
//   · 日志与结果能回传给宿主
// 三条都成立，热更新链路才是通的。
const DemoSource = `package main

import (
	"fmt"

	"autogov1/v2/sdk/ios/script/api"
)

// main 是脚本入口：Runner 负责调用它。
func main() {
	api.Log("脚本开始执行")
	api.Log("参数 name =", api.ParamString("name"))

	// 1) 打一次能力探测（原生 runtime.capabilities）
	caps, err := api.Call("runtime.capabilities", nil)
	if err != nil {
		api.Log("能力探测失败:", err)
	} else {
		api.Log("能力探测:", caps)
	}

	// 2) 抓一帧（原生 capture.image，返回的是图片数据）
	shot, err := api.Call("capture.json", map[string]any{"maxSize": 512, "jpeg": true, "quality": 0.6})
	if err != nil {
		api.Log("采集失败:", err)
	} else {
		// 只打字节数与尺寸，不打 base64（日志会爆）
		api.Log(fmt.Sprintf("采集成功: %v 字节 %vx%v",
			shot["bytes"], shot["width"], shot["height"]))
	}

	// 3) 投一次点击（走 motion.frame，按下+抬起在原生里完成）
	if err := api.Sleep(50); err != nil {
		api.Log("等待被中断:", err)
	}
	x := api.ParamFloat("x")
	y := api.ParamFloat("y")
	if x == 0 {
		x = 0.5
	}
	if y == 0 {
		y = 0.5
	}
	if _, err := api.Call("motion.frame", map[string]any{
		"contacts": []map[string]any{{"id": 0, "x": x, "y": y}},
	}); err != nil {
		api.Log("按下失败:", err)
	}
	if _, err := api.Call("motion.frame", map[string]any{"contacts": []map[string]any{}}); err != nil {
		api.Log("抬起失败:", err)
	} else {
		api.Log(fmt.Sprintf("已在 (%.2f, %.2f) 投一次点击", x, y))
	}

	api.SetResult(map[string]any{"ok": true, "x": x, "y": y})
	api.Log("脚本执行完成")
}
`
