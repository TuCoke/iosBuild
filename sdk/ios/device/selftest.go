// Package device 的自检能力：把"符号存在"升级成"真做一次，看结果"。
//
// 与 Capabilities 的区别（这是本文件存在的理由）：
//
//	Capabilities  → 只查 dlsym 能不能解析到符号，真机上符号在但没授权时照样 true
//	SelfTest      → 真投一次点击、真抓一帧，报告"提交成功"与"可观测结果"
//
// 两者都要有：Capabilities 快、无副作用，适合启动时判断环境；
// SelfTest 慢（约 100ms）、会真的动一下屏幕，适合在真机上做验收。
package device

import (
	"context"
	"time"

	"autogov1/v2/sdk/ios/internal/native"
)

// Check 是一项自检的结果。
//
// Detail 必须能被人读懂：出问题时它要说清是"符号缺失"、"提交失败"还是"提交成功但结果不对"。
type Check struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	// Width/Height 只在采集自检里有意义，其他项为 0。
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

// SelfTestResult 是一次完整自检的结果。
type SelfTestResult struct {
	// Touch 是"投一次点击"的结果：提交成功不代表界面响应，Detail 里会写清。
	Touch Check `json:"touch"`
	// Capture 是"抓一帧"的结果：全黑帧会被判为失败（能调但不生效）。
	Capture Check `json:"capture"`
	// Injected 只有 Touch 与 Capture 都成功时为 true。
	Injected bool `json:"injected"`
}

// SelfTest 跑一次自检：在给定位置（归一化坐标，传 0.5/0.5 即屏幕中心）投一次点击，
// 并抓一帧看采集是否真的出图。
//
// 这里不走 bridge.Do（它把错误吞成 false）——自检的意义就在于把失败原因带出来，
// 所以直接用 native.Call，错误原样返回。
//
// 超时给 15 秒：采集在首次调用时要建 render server 连接，比普通调用慢。
func SelfTest(ctx context.Context, x, y float64) (SelfTestResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out SelfTestResult
	if err := native.Call(callCtx, "runtime.selftest", map[string]any{"x": x, "y": y}, &out); err != nil {
		return SelfTestResult{}, err
	}
	return out, nil
}

// SelfTestCenter 是 SelfTest 的常用形态：在屏幕中心投一次点击。
func SelfTestCenter(ctx context.Context) (SelfTestResult, error) {
	return SelfTest(ctx, 0.5, 0.5)
}
