# iOS 真机工作进程动态库加载修复（2026-11-11）

## 问题与已确认范围

用户提供的真机日志显示：宿主点击注入计数 0→1、宿主截图成功、AX 采集 73 个节点；独立工作进程在加载 SDK 时失败：`Library not loaded: @rpath/libimgui.dylib`。这不能记为脚本工作进程已经通过真机验收。

`004752f` 已把执行体搬到可写的 Documents，但仍有三个缺口：真机包把 ImGui 放进 `App/Runtime/`，复制逻辑却只查 App 根目录；仅按文件大小复用缓存；复制失败直接忽略。另外，OpenCV 已由 SDK 直接链接，PPOCR/YOLO 等引擎也存在 `@rpath` 间接依赖，绝对路径 dlopen 引擎本身不能替代这些依赖的准备。

## 本次改动

- 保持原有 `worker.Run`、进程串行闸门、看护、结果回执与 Go SDK 调用协议。
- 将文件准备集中在 `script/worker/prepare.go`，不修改 Agent/MCP/安卓逻辑。
- 读取当前实际 SDK 和执行体的 Mach-O 加载命令，收集非系统依赖及其间接依赖；仅读取头和命令，不展开大型 SDK 的完整符号表。
- 优先查找 App `Runtime/`，并兼顾根目录、引用者目录及 `Frameworks/`；模拟器根目录布局继续支持。
- 纳入 Runtime 中动态加载的识别库及其依赖。系统框架和 `/usr/lib/` 仍由系统解析，不复制。
- 以 SDK、执行体和所有依赖的 SHA256 生成 `Documents/Worker/<摘要>/`。相同大小的新文件也能识别，旧版本运行目录不被覆盖。
- 文件流式复制到临时文件，核对摘要、同步并设置执行权限后原子改名。缓存损坏或删除会补齐，读取/写入/chmod 失败明确返回，准备完成前不启动脚本。
- 校验 Mach-O 类型、CPU、平台和同名库冲突；拒绝真机/模拟器混装、缓存目录软链接及未知依赖布局。
- 启动时传入 `--bundle`，原生资源定位优先使用原 App 根目录；`runtime.paths.bundle` 同步使用该位置，避免把 Documents 工作目录当成 App。
- 文件准备后重新计算剩余任务期限。

当前库布局是平铺 dylib；任意嵌套 Framework、任意绝对第三方依赖及 fat Mach-O 没有被偷偷当作兼容。遇到未知布局明确报发布产物错误。旧版本运行目录暂不自动清理，避免删除潜在仍被使用的文件；长期缓存清理需独立维护策略。

## 文件

```text
v2/sdk/ios/
├── script/worker/worker.go       启动顺序、剩余期限及 --bundle
├── script/worker/prepare.go      依赖图、内容缓存、完整性及原子准备
├── script/worker/prepare_test.go 文件准备的定向回归
├── internal/native/bundle_ios.m 原 App 资源根定位
└── internal/native/runtime_ios.m 运行路径返回
```

`v2/ios/sdk/ios/` 及 GitHub 独立验证仓保持同源。先前已拉取的 `capture_ios.m` 诊断增强也同步到本轮 GitHub 构建基线。

## 验证

本地先检查再提交：

- `CGO_ENABLED=0 go test ./sdk/ios/script/... -count=1 -timeout=120s` 通过。
- `go run ./cmd/precheck` 通过，包含 101 个 Go 文件与 25 个原生/包装层文件。
- Windows 纯 Go build、vet，以及 Darwin/arm64 纯 Go交叉编译通过。
- 新增 9 个用例覆盖 Runtime 布局、模拟器根库、直接/间接依赖、同大小升级、缓存丢失/损坏、缺依赖、复制失败、平台错误/取消、原子失败保留旧文件、软链接隔离。Windows 不具备软链接权限时该项显式跳过，需由 Unix CI 核对。

这些是文件准备和编译回归，不是伪造设备操作。GitHub 原生构建、模拟器以及新的真机结果待后续补充。用户需更新包含本次修正的 SDK/安装包，并确认 `native-diagnostics-worker.json` 中工作进程、截图、AX 与脚本结果，不以宿主点击成功替代子进程成功。
