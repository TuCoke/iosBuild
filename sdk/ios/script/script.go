// Package script 是端上 Go 脚本解释器（yaegi）。
//
// 【为什么必须要有它】没有解释器，改一行脚本就得重新打包安装 ——
// 线上调一次采集坐标要等一次发版，这条链路根本没法用。
// 有了它，脚本作为"数据"下发，App 只管执行，热更新才成立。
//
// 【选择 yaegi 而不是内置 JS 引擎】脚本要直接调 SDK 的全部原生能力，
// 用 Go 写脚本就是"零翻译"地调 Go 函数；JS 引擎还得再维护一层绑定表。
// 代价是解释器体积（约几 MB）与首次编译开销，这两点在 iOS 上可接受。
//
// 【已知限制，先写在最前面】
//  1. 解释执行无法抢占：脚本里写死循环，宿主杀不掉（yaegi 没有中断入口）。
//     对策是把"长任务"拆成多次调用，宿主侧用超时+看门狗判断卡死。
//  2. 解释执行里不能用 cgo / unsafe：脚本只能调我们导出的 api 包。
//  3. 首次 Eval 有编译开销（几十~几百 ms），所以 Runner 复用同一个解释器实例。
package script

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"

	"autogov1/v2/sdk/ios/script/api"
)

// Options 控制一次脚本运行。
type Options struct {
	// Params 是脚本可读的参数（api.Param 取）。
	Params map[string]any
	// Timeout 是软超时：超过后 api.Call / api.Sleep 会返回超时错误，
	// 但已经在跑的纯计算代码无法被打断（见包注释的限制 1）。
	Timeout time.Duration
	// NoStdlib 关闭标准库符号表（fmt/strings/time 等都用不了）。
	//
	// 默认是"加载"：脚本写日志、拼字符串几乎一定会用到 fmt/strings，
	// 关掉会让人第一行就撞墙。代价是解释器多占几 MB，iOS 上可以接受。
	NoStdlib bool
}

// Result 是一次脚本执行的结果。
type Result struct {
	// Value 是脚本通过 api.SetResult 交回的值（没交回就是 nil）。
	Value any
	// Logs 是脚本 api.Log 出来的行。
	Logs []string
	// Elapsed 是执行耗时。
	Elapsed time.Duration
}

// Runner 持有一个可复用的解释器实例。
//
// 复用而不是每次新建：新建要重新装载符号表（标准库那部分尤其慢），
// 一次运行几十毫秒的开销对热更新来说是灾难。
type Runner struct {
	mu     sync.Mutex
	interp *interp.Interpreter
	opts   Options
}

// New 建一个 Runner。第一次 Eval 时才真正装载符号表。
func New(opts Options) *Runner {
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	return &Runner{opts: opts}
}

// ensure 惰性初始化解释器。
func (r *Runner) ensure() *interp.Interpreter {
	if r.interp != nil {
		return r.interp
	}
	i := interp.New(interp.Options{})
	if !r.opts.NoStdlib {
		i.Use(stdlib.Symbols)
	}
	i.Use(api.Symbols)
	r.interp = i
	return i
}

// Run 执行一段脚本源码。
//
// 脚本的入口约定为 func main()：源码里定义 main，Run 负责调用它。
// 这样脚本可以有自己的辅助函数，不用把所有逻辑塞在一个 Eval 里。
func (r *Runner) Run(ctx context.Context, source string) (Result, error) {
	if source == "" {
		return Result{}, fmt.Errorf("脚本内容为空")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	runCtx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
	defer cancel()

	// 让 api 层知道这一轮的上下文与参数。
	api.Reset(runCtx, r.opts.Params)

	started := time.Now()
	i := r.ensure()

	if _, err := i.Eval(source); err != nil {
		return Result{Logs: api.Logs(), Elapsed: time.Since(started)}, fmt.Errorf("脚本编译失败: %w", err)
	}
	// 入口调用：没有 main 就当成顶层语句脚本（Eval 已经执行完了）。
	if _, err := i.Eval("main()"); err != nil {
		if isUndefined(err, "main") {
			return Result{Value: api.Result(), Logs: api.Logs(), Elapsed: time.Since(started)}, nil
		}
		return Result{Value: api.Result(), Logs: api.Logs(), Elapsed: time.Since(started)}, fmt.Errorf("脚本执行失败: %w", err)
	}
	return Result{Value: api.Result(), Logs: api.Logs(), Elapsed: time.Since(started)}, nil
}

// isUndefined 判断错误是不是"没有 main 这个符号"。
// yaegi 的报错文本不稳定（版本间改过），所以按子串判断。
func isUndefined(err error, name string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "undefined: "+name) || strings.Contains(msg, "not found: "+name)
}
