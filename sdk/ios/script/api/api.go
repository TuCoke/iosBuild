// Package api 是端上脚本能看到的全部入口。
//
// 【为什么只暴露一个 Call，而不是把 222 个 API 都挂上来】
// yaegi 的符号表要手写 reflect.Value，逐个挂 222 个函数既啰嗦又容易漏、容易漂移；
// 而脚本真正需要的是"调一个原生能力"这一件事 —— Call(op, args)
// 就能打到全部 62 个原生方法（motion.frame / capture.image / app.launch …），
// 也和我们服务端 JS 模板里的 tools.call 形状一致，脚本在两边的写法能对上。
//
// 需要类型化封装（Click/Swipe/Screenshot…）时再加薄壳，不用改调用约定。
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"autogov1/v2/sdk/ios/internal/native"
)

// Args 是脚本传参的简写。用 map[string]any 而不是泛型，
// 是因为 yaegi 对泛型的支持还不完整。
type Args = map[string]any

// session 保存当前这次运行的上下文。脚本是单线程执行的，
// 但 api 是包级状态，用锁保护一下更稳（脚本里起 goroutine 也不会立刻炸）。
type session struct {
	mu     sync.Mutex
	ctx    context.Context
	params map[string]any
	logs   []string
	result any
}

var current = &session{ctx: context.Background()}

// Reset 由 Runner 在每次执行前调用：清空上一轮的日志与结果。
func Reset(ctx context.Context, params map[string]any) {
	current.mu.Lock()
	defer current.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	current.ctx = ctx
	current.params = params
	current.logs = nil
	current.result = nil
}

// Logs 取回本次运行的日志（Runner 用于回传给宿主）。
func Logs() []string {
	current.mu.Lock()
	defer current.mu.Unlock()
	out := make([]string, len(current.logs))
	copy(out, current.logs)
	return out
}

// Result 取回脚本通过 SetResult 交回的结果。
func Result() any {
	current.mu.Lock()
	defer current.mu.Unlock()
	return current.result
}

// Log 往宿主日志里写一行。参数按 fmt.Sprint 拼，和 JS 模板的 log() 对齐。
func Log(args ...any) {
	line := ""
	for i, a := range args {
		if i > 0 {
			line += " "
		}
		line += fmt.Sprint(a)
	}
	current.mu.Lock()
	current.logs = append(current.logs, line)
	current.mu.Unlock()
}

// Param 读一个脚本参数；没有就返回 nil。
func Param(name string) any {
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.params == nil {
		return nil
	}
	return current.params[name]
}

// ParamString 读字符串参数（缺失或类型不符返回空串）。
func ParamString(name string) string {
	if v, ok := Param(name).(string); ok {
		return v
	}
	return ""
}

// ParamFloat 读数值参数（缺失返回 0）。
func ParamFloat(name string) float64 {
	switch v := Param(name).(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}

// SetResult 交回脚本结果。整个脚本只该调用一次。
func SetResult(v any) {
	current.mu.Lock()
	current.result = v
	current.mu.Unlock()
}

// Sleep 让脚本睡一会儿（毫秒）。脚本里的等待一律走这里，
// 这样宿主给的时间预算还能生效。
func Sleep(ms int) error {
	if ms <= 0 {
		return nil
	}
	ctx := current.ctx
	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Call 调一个原生能力，参数与返回都是 JSON 形状的 map。
//
// 例：
//
//	api.Call("motion.tap", api.Args{"x": 0.5, "y": 0.5})
//	img, err := api.Call("capture.image", api.Args{"maxSize": 1024})
//
// 失败时返回 error —— 脚本里要处理，不能当成零值继续跑。
func Call(op string, args Args) (map[string]any, error) {
	if op == "" {
		return nil, fmt.Errorf("缺少能力名")
	}
	input := map[string]any(args)
	if input == nil {
		input = map[string]any{}
	}
	var out map[string]any
	ctx := current.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := native.Call(callCtx, op, input, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// CallString 是 Call 的便捷形式：只要其中一个字符串字段。
func CallString(op string, args Args, field string) (string, error) {
	out, err := Call(op, args)
	if err != nil {
		return "", err
	}
	if v, ok := out[field].(string); ok {
		return v, nil
	}
	return "", nil
}

// MustCall 失败即中断脚本（配合 defer/recover 用，或直接让 Runner 报错）。
func MustCall(op string, args Args) map[string]any {
	out, err := Call(op, args)
	if err != nil {
		panic(fmt.Sprintf("%s 失败: %v", op, err))
	}
	return out
}
