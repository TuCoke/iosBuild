# iOS Go 脚本运行

这是 iOS SDK 的 Go / Yaegi 解释器，不是服务器 JS 工作流或手机 JS 引擎。每次任务创建独立解释器与参数/日志/结果 Session；同一个 Runner 串行，任务 context 取消或超时后不复用旧解释器。

```go
runner := script.New(script.Options{Timeout: 60*time.Second})
result, err := runner.Run(ctx, source)                 // 单个源码字符串
result, err = runner.RunFile(ctx, "/path/main.go")    // 所在目录作为工程
result, err = runner.RunZip(ctx, archiveBytes, "")    // 默认 main.go
result, err = runner.RunProject(ctx, sourceFS, "main.go")
```

工程可含 go.mod、本地包、多文件 main 和 go:embed 的 string、[]byte、embed.FS。ZIP 可带一层共同的外层目录。入口参数是工程内相对路径。只读 embed 文件系统实现 ReadFile、ReadDir、Open；不改变宿主进程工作目录，不自动铺设真实文件，不自动下载第三方模块。

原生 SDK 符号生成（从对应模块根运行）：

- 主模块：`go run ./tools/sdk-symbolgen -root .`
- iOS 独立镜像：`go run ./cmd/symbolgen -root .`

注册公开函数、类型、常量和变量；构造器返回指针不再被省略。SDK 包采用自有 `autogov1/v2/sdk/ios/...` 路径，Options.ImportAliases 可显式注册同一能力的其他 import 路径，不承诺闭源 SDK 全量兼容。uiacc.New 在解释器中绑定任务 context，任务结束释放控件采集引用。

限制：单源码 4 MiB、工程最多 1024 文件/32 MiB、日志 1 MiB/10000 行、JSON 参数/结果各 4 MiB。文件、解压、缺失资源和编译错误向上返回。源码字符串包含 go:embed 时需要改用工程入口。

Go context 可以中断解释器计算与 api.Sleep，不能强行打断正在执行的 C、系统 API 或所有宿主 Go 函数。需要可强制终止时，交给任务进程执行并由宿主看护；不得把超时响应当作动作已经停止。标准库导出不是沙箱，只供可信任务使用。

`remote.go` 已修复摘要格式、context 取消、缓存越界和发布次序。下载或哈希失败可以读取已有缓存；新脚本已经执行后失败时不会重放旧脚本，避免重复动作。仅成功执行的远端版本才原子替换缓存。这不是业务动作回滚。

宿主 `script.run` 通过 `script/worker` 使用 posix_spawn 启动 `Runtime/app`，传入 Go 文件或 ZIP、参数文件和结果文件；任务完成后进程退出。宿主使用 waitpid 状态确认回收，超时会终止该工作进程。Go SDK 的 Runner 仍可嵌入执行，原生函数的硬中止保证只来自独立工作进程。

`apkctl` 使用样本的本机 8990 TCP 二进制协议连接宿主 JavaScriptCore；解释器为每次运行绑定独立 Session，关闭时释放回调和未完成请求。完整的原生 JS UI 结构/动作桥仍未提供。
