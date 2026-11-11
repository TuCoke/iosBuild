// Package script 执行 Go 脚本和工程。每次任务独立解释器，原生阻塞仍需进程看护兜底。
package script

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"

	"autogov1/v2/sdk/ios/apkctl"
	"autogov1/v2/sdk/ios/ppocr"
	"autogov1/v2/sdk/ios/script/api"
	"autogov1/v2/sdk/ios/script/embedfs"
	"autogov1/v2/sdk/ios/uiacc"
	"autogov1/v2/sdk/ios/yolo"
)

type Options struct {
	Params        map[string]any
	Timeout       time.Duration
	NoStdlib      bool
	ImportAliases map[string]string
}
type Result struct {
	ProcessID int // 独立工作进程的 PID；内嵌执行时为 0。
	Value     any
	Logs      []string
	Elapsed   time.Duration
}
type Runner struct {
	opts Options
	gate chan struct{}
}

func New(opts Options) *Runner {
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	return &Runner{opts: opts, gate: make(chan struct{}, 1)}
}
func (r *Runner) execute(ctx context.Context, filesystem fs.FS, entry, source string) (result Result, err error) {
	if ctx == nil {
		return result, fmt.Errorf("脚本 context 不能为空")
	}
	runCtx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
	defer cancel()
	select {
	case r.gate <- struct{}{}:
		defer func() { <-r.gate }()
	case <-runCtx.Done():
		return result, runCtx.Err()
	}
	if err = runCtx.Err(); err != nil {
		return result, err
	}
	session, err := api.NewSession(runCtx, r.opts.Params)
	if err != nil {
		return result, err
	}
	started := time.Now()
	defer func() {
		result.Logs = session.Logs()
		result.Value = session.Result()
		result.Elapsed = time.Since(started)
		if v := recover(); v != nil {
			err = fmt.Errorf("脚本宿主异常: %v", v)
		}
	}()
	i := interp.New(interp.Options{GoPath: ".", SourcecodeFilesystem: filesystem, Stdout: session, Stderr: session})
	if !r.opts.NoStdlib {
		if err = i.Use(stdlib.Symbols); err != nil {
			return result, err
		}
	}
	if err = i.Use(session.Symbols()); err != nil {
		return result, err
	}
	// 每次运行复制绑定表，只覆盖需要任务生命周期的构造器，不能改动全局注册表。
	sdkSymbols := make(interp.Exports, len(api.SDKPackages))
	for key, values := range api.SDKPackages {
		sdkSymbols[key] = values
	}

	control := apkctl.NewSession(runCtx)
	defer control.Close()
	const controlKey = "autogov1/v2/sdk/ios/apkctl/apkctl"
	controlSymbols := make(map[string]reflect.Value, len(api.SDKPackages[controlKey]))
	for name, value := range api.SDKPackages[controlKey] {
		controlSymbols[name] = value
	}
	controlSymbols["Eval"] = reflect.ValueOf(control.Eval)
	controlSymbols["SetCallback"] = reflect.ValueOf(control.SetCallback)
	controlSymbols["RegEvent"] = reflect.ValueOf(control.RegEvent)
	sdkSymbols[controlKey] = controlSymbols

	const uiKey = "autogov1/v2/sdk/ios/uiacc/uiacc"
	uiSymbols := make(map[string]reflect.Value, len(api.SDKPackages[uiKey]))
	for name, value := range api.SDKPackages[uiKey] {
		uiSymbols[name] = value
	}
	uiSymbols["New"] = reflect.ValueOf(func() *uiacc.Uiacc { return uiacc.NewWithContext(runCtx) })
	sdkSymbols[uiKey] = uiSymbols
	// 模型也属于任务资源：脚本遗漏 Close 时，在任务 context 结束后回收自己的实例。
	for key, constructor := range map[string]reflect.Value{
		"autogov1/v2/sdk/ios/ppocr/ppocr": reflect.ValueOf(func(version string) *ppocr.Ppocr {
			engine := ppocr.New(version)
			context.AfterFunc(runCtx, engine.Close)
			return engine
		}),
		"autogov1/v2/sdk/ios/yolo/yolo": reflect.ValueOf(func(version string, threads int, param, bin, labels string) *yolo.Yolo {
			engine := yolo.New(version, threads, param, bin, labels)
			context.AfterFunc(runCtx, engine.Close)
			return engine
		}),
	} {
		values := make(map[string]reflect.Value, len(sdkSymbols[key]))
		for name, value := range sdkSymbols[key] {
			values[name] = value
		}
		values["New"] = constructor
		sdkSymbols[key] = values
	}

	if err = i.Use(sdkSymbols); err != nil {
		return result, err
	}
	if err = i.Use(interp.Exports{"autogov1/v2/sdk/ios/script/embedfs/embedfs": {"FS": reflect.ValueOf((*embedfs.FS)(nil)), "New": reflect.ValueOf(embedfs.New)}}); err != nil {
		return result, err
	}
	for alias, target := range r.opts.ImportAliases {
		var symbols interp.Exports
		for key, values := range sdkSymbols {
			if key == target+"/"+path.Base(target) {
				symbols = interp.Exports{alias + "/" + path.Base(target): values}
				break
			}
		}
		if symbols == nil {
			return result, fmt.Errorf("脚本别名目标未注册: %s", target)
		}
		if err = i.Use(symbols); err != nil {
			return result, err
		}
	}
	// Yaegi 有取消检查；已经进入的原生函数不一定立即停止，因此超时后不能复用解释器。
	if entry != "" {
		_, err = i.EvalPathWithContext(runCtx, entry)
	} else {
		_, err = i.EvalWithContext(runCtx, source)
	}
	if err != nil {
		return result, fmt.Errorf("脚本执行失败: %w", err)
	}
	if err = session.Error(); err != nil {
		return result, err
	}
	return result, nil
}
func (r *Runner) Run(ctx context.Context, source string) (Result, error) {
	if source == "" {
		return Result{}, fmt.Errorf("脚本内容为空")
	}
	if len(source) > 4<<20 {
		return Result{}, fmt.Errorf("脚本超过 4 MiB")
	}
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", source, parser.ParseComments)
	if err != nil {
		return Result{}, fmt.Errorf("脚本解析失败: %w", err)
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//go:embed ") {
				return Result{}, fmt.Errorf("包含 go:embed 的脚本请使用 RunFile、RunProject 或 RunZip 提供资源")
			}
		}
	}

	return r.execute(ctx, nil, "", source)
}
