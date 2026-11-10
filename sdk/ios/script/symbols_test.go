package script

import (
	"context"
	"strings"
	"testing"

	"autogov1/v2/sdk/ios/script/api"
)

// TestScriptImportsSDK 验证脚本能直接 import 我们的 SDK 包 ——
// 这是与参考实现"脚本写法一致"的判据：
// 他们写 motion.Click / ppocr.New，我们也要能写 motion.Click，
// 而不是只能 api.Call("motion.frame", …)。
func TestScriptImportsSDK(t *testing.T) {
	src := `package main

import (
	"autogov1/v2/sdk/ios/motion"
	"autogov1/v2/sdk/ios/script/api"
)

func main() {
	motion.Click(500, 800, 0)
	api.Log("motion.Click 已通过符号表调用")
	api.SetResult(map[string]any{"ok": true})
}
`
	runner := New(Options{})
	res, err := runner.Run(context.Background(), src)
	if err != nil {
		t.Fatalf("脚本执行失败: %v", err)
	}
	joined := strings.Join(res.Logs, "\n")
	if !strings.Contains(joined, "motion.Click") {
		t.Fatalf("motion 包没被解析到，日志:\n%s", joined)
	}
	result, _ := res.Value.(map[string]any)
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("脚本结果不对: %#v", res.Value)
	}
}

// TestSymbolTableNotEmpty 保证生成器产出不是空的（漏跑生成器时立刻发现）。
func TestSymbolTableNotEmpty(t *testing.T) {
	if api.SDKPackageCount() < 3 {
		t.Fatalf("符号表里只有 %d 个包，生成器可能没跑", api.SDKPackageCount())
	}
}
