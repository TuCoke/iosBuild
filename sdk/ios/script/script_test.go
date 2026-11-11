package script

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestRunDemo 是解释器的端到端验收：不依赖设备，任何平台都能跑。
//
// 它验证的是"解释器 + 符号表 + 日志回传"这条链路；
// 脚本里调原生能力在非 iOS 平台必然失败（native 桩返回不支持），
// 但脚本必须继续跑完并把失败写进日志 —— 这正是我们要保住的语义：
// 一次能力调用失败，不能让整段脚本静默消失。
func TestRunDemo(t *testing.T) {
	runner := New(Options{Params: map[string]any{"name": "测试"}, Timeout: 20 * time.Second})
	res, err := runner.Run(context.Background(), DemoSource)
	if err != nil {
		t.Fatalf("脚本执行失败: %v", err)
	}
	if len(res.Logs) == 0 {
		t.Fatal("没有拿到任何日志")
	}
	joined := strings.Join(res.Logs, "\n")
	for _, want := range []string{"脚本开始执行", "参数 name = 测试", "脚本执行完成"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("日志里缺少 %q，实际日志:\n%s", want, joined)
		}
	}
	result, ok := res.Value.(map[string]any)
	if !ok {
		t.Fatalf("SetResult 的值类型不对: %T", res.Value)
	}
	if okFlag, _ := result["interpreterCompleted"].(bool); !okFlag {
		t.Fatalf("解释器未完整执行: %#v", result)
	}
	if res.Elapsed <= 0 {
		t.Fatal("耗时没有记录")
	}
}

// TestRunSyntaxError 编译错误必须带出原因，不能静默。
func TestRunSyntaxError(t *testing.T) {
	runner := New(Options{})
	if _, err := runner.Run(context.Background(), "package main\nfunc main() { 这不是 Go }\n"); err == nil {
		t.Fatal("语法错误没有被报出来")
	}
}

// TestRunEmpty 空脚本直接拒绝。
func TestRunEmpty(t *testing.T) {
	runner := New(Options{})
	if _, err := runner.Run(context.Background(), ""); err == nil {
		t.Fatal("空脚本应当报错")
	}
}

// TestRunParamsAndResult 参数读取与结果回传各字段都对。
func TestRunParamsAndResult(t *testing.T) {
	src := `package main

import "autogov1/v2/sdk/ios/script/api"

func main() {
	api.Log("x =", api.ParamFloat("x"))
	api.SetResult(map[string]any{"doubled": api.ParamFloat("x") * 2})
}
`
	runner := New(Options{Params: map[string]any{"x": 2.5}})
	res, err := runner.Run(context.Background(), src)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	result, _ := res.Value.(map[string]any)
	if got, _ := result["doubled"].(float64); got != 5 {
		t.Fatalf("期望 doubled=5，实际 %#v", result["doubled"])
	}
}

// TestRunCacheMissing 没有缓存时要报得清楚（而不是返回空脚本）。
func TestRunCacheMissing(t *testing.T) {
	runner := New(Options{})
	if _, err := runner.RunCache(context.Background(), "不存在的脚本"); err == nil {
		t.Fatal("没有缓存时应当报错")
	}
}
