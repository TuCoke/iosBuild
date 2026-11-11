// Runtime/app 执行一个 Go 文件或 ZIP 工程后退出。
// 参考样本的实际 GoMain 入口会切到执行目录，再选择 runGoFile/runZip；
// 不能仅凭宿主出现过 8989 URL，就把这个执行体误认成常驻 HTTP 源码服务。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"autogov1/v2/sdk/ios/files"
	"autogov1/v2/sdk/ios/script"
	"autogov1/v2/sdk/ios/script/worker"
)

func main() { runWithMainLoop(func() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }) }
func run(args []string, stdout, stderr io.Writer) int {
	options := flag.NewFlagSet("Runtime/app", flag.ContinueOnError)
	options.SetOutput(stderr)
	resultPath := options.String("result", "", "宿主结果文件")
	paramsPath := options.String("params", "", "宿主参数 JSON 文件")
	timeout := options.Duration("timeout", 120*time.Second, "脚本执行期限")
	if err := options.Parse(args); err != nil {
		return 2
	}
	if options.NArg() != 1 {
		fmt.Fprintln(stderr, "用法: Runtime/app [--result 文件] [--params 文件] [--timeout 2m] 脚本.go|工程.zip")
		return 2
	}
	filename, err := filepath.Abs(options.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *resultPath != "" {
		*resultPath, err = filepath.Abs(*resultPath)
		if err != nil {
			return 2
		}
	}
	var params map[string]any
	if *paramsPath != "" {
		raw, e := os.ReadFile(*paramsPath)
		if e != nil {
			fmt.Fprintln(stderr, e)
			return 2
		}
		if len(raw) > 4<<20 {
			fmt.Fprintln(stderr, "参数超过 4 MiB")
			return 2
		}
		if e = json.Unmarshal(raw, &params); e != nil {
			fmt.Fprintln(stderr, e)
			return 2
		}
	}
	// 工作进程独占 cwd；主宿主/并发 Go SDK 调用不会被修改。
	if executable, e := os.Executable(); e == nil {
		root := filepath.Dir(executable)
		if e = os.Chdir(root); e != nil {
			fmt.Fprintln(stderr, e)
			return 2
		}
		files.SetRoot(root)
	}
	res, err := script.New(script.Options{Params: params, Timeout: *timeout}).RunFile(context.Background(), filename)
	reply := worker.Reply{OK: err == nil, Logs: res.Logs, Value: res.Value, ElapsedMS: res.Elapsed.Milliseconds()}
	code := 0
	if err != nil {
		reply.Error = err.Error()
		code = 1
	}
	body, e := json.Marshal(reply)
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 2
	}
	if *resultPath != "" {
		temp := *resultPath + ".tmp"
		if e = os.WriteFile(temp, body, 0600); e == nil {
			e = os.Rename(temp, *resultPath)
		}
		if e != nil {
			fmt.Fprintln(stderr, e)
			return 2
		}
	}
	fmt.Fprintln(stdout, string(body))
	return code
}
