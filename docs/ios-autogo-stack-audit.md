# AutoGo iOS 技术栈与当前 SDK 对照核查

> 当前总表：[AutoGo iOS 对标进度与缺项](ios-autogo-status.md)。本文件保留对应分析时点的细节，完成状态以总表为准。

> 后续实现已继续修正：当前输入、视频、AX、应用和识别代码状态见 [本轮对齐记录](ios-autogo-sdk-alignment.md)。本文未修复清单属于该轮核查时点，不能继续当作最新状态。

更新：2026-11-11。对照基线为 agent-cgo 的 `5d6bd31` 及本轮尚未提交的修正。本文是当前结论；旧文档中的“完全对标”“缺项 0”“全部可用”不能作为验收依据。

## 1. 结论与证据边界

可以参考它的分层、私有 API 调用方式、脚本绑定和资源生命周期。当前不能宣称我们的 SDK 已完整复现其全部能力，更不能用函数数量或模拟器探针代替真实设备操作。

本次读取的是本地提供的文件，未运行 AutoGo 可执行文件，也未修改其二进制：

- 主样本：`AutoGo-ios/resources/libs/ios/libgoeval.dylib`，41,672,232 字节。
- SHA256：`b4e157737a9bba0de49fd98ad5d9b5e30519118cacbd63df46e807b0e6f5af48`。
- 其他输入：`AutoGo-ios/resources/libs/ios/` 的识别/图形库、`resources/META-INF/ios.toml`，解包后的 App 与 Runtime 文件。
- 方法：Mach-O 头与加载命令、导入/导出、Go 构建信息和 pclntab、ARM64 调用点、Objective-C selector 和属性到 JSON 字段的数据流。

证据分为：

| 层级 | 可以证明 | 不能证明 |
| --- | --- | --- |
| 名称或字符串 | 文件中存在相关名称 | 函数一定被调用、功能可用 |
| 实际调用及参数流 | 某条路径调用了哪些函数、参数如何处理 | 所有系统版本均能执行 |
| 我们源码已连接 | Go → cgo → 原生分派有代码路径 | 已通过 Apple 编译、设备授权或业务验证 |
| 设备实测 | 指定系统、设备、步骤的结果 | 所有 iOS 版本和 App 都适用 |

样本 `libgoeval.dylib` 的 Mach-O filetype 实际是 **MH_EXECUTE**，并非因扩展名而成为 MH_DYLIB。我们的构建使用 c-archive + clang `-dynamiclib`，两者的产物形态不能写成“完全一致”。应分别核对资源包、最终安装包和加载行为，不能随意替换文件。

## 2. 整体分层与运行原理

```text
AutoGo iOS（由已见文件与调用关系还原）
  ObjC / UIKit / WebKit 外壳
    ├── 路径拼接、动态加载、界面与宿主通信
    ├── Runtime/app：任务执行体，8989 状态/任务接口
    └── Go 能力层 + Yaegi 解释器
          ├── SDK 包和脚本符号绑定
          ├── C Bridge → Objective-C / C / C++
          ├── IOKit / AXRuntime / SpringBoard / IOSurface / VideoToolbox
          └── NCNN / OpenCV / OCR / YOLO / cimgui + Metal
```

我们的对应代码：

```text
v2/sdk/ios/                         供 v2 主模块消费的能力源码
v2/tools/sdk-symbolgen/             脚本符号生成器
v2/ios/                            当前独立 iOS 构建源码镜像
├── cmd/ioshost/                    导出自有 C ABI、方法分派
├── cmd/runtimeapp/                 独立任务进程
├── cmd/symbolgen/                  镜像内的生成器
├── host/ios/Sources/               ObjC 壳、SDKLoader、任务管理
├── host/ios/objc/                  OCR 和 ImGui 的 ObjC++ 胶水
└── sdk/ios/                        镜像中的 SDK
```

本轮只同步相关 SDK 与生成器改动，没有移动 Agent 或重新规划安卓。两个 SDK 目录目前仍是两份物理源码；后续发布需明确唯一来源及同步校验，不能假定它们天然一致。`v2/ios/go.mod` 与主模块同名，也不意味着独立 iOS 构建能直接带入主模块的全部 Agent 源码。

## 3. 全部能力分组对照

| 分组 | AutoGo 样本技术与原理 | 当前我们的实现 | 差异/未验收点 |
| --- | --- | --- | --- |
| 外壳与加载 | ObjC、UIKit、WebKit、dlopen、spawn | `host/ios/Sources`、`cmd/ioshost` | 结构接近；候选路径、产物类型和初始化语义未证明相同 |
| 任务体 | Runtime/app、8989/status 和 task | `cmd/runtimeapp` | 接口形状相似；鉴权、请求取消、进程看护仍需核查 |
| 触摸 | IOHID 父 digitizer + finger 子事件、派发 | `motion` + `touch_ios.m`，完整触点帧 | Go 简版、底层帧、C Bridge 有不同状态路径；C 逐指桥有问题，见第 7 节 |
| 键盘/中文 | IOHID keyboard / Unicode | `ime` + 原生 HID | 私有 ABI、中文组合及实际目标 App 待真机验证 |
| 截图 | 渲染服务器、IOSurface、图像转换 | `capture` + `capture_ios.m` | 方向、scale、黑屏、权限与受保护画面待验证 |
| 连续帧/编码 | Raw frame 回调、VideoToolbox H.264、C→Go 回调 | `video` 提供有界队列、Go 拉取、Annex B | Go 路径有实现；C 回调兼容桥未接通，不能说等价 |
| 控件树 | AX 私有对象缓存、5039 快照、坐标补采、共享后台刷新 | 本轮替换原错误采集器，并增加共享缓存 | 原生新路径尚未 Apple 编译/真机验证；AX 激活与权限差异见第 4 节 |
| 应用管理 | SpringBoard / FrontBoard / LSApplicationWorkspace | `app` + `app_ios.m` | 启动受理不等于已切前台；安装、清数据等须逐项验收 |
| 设备/系统 | UIDevice、MobileGestalt、sysctl、SpringBoard 等 | `device/system` + 原生分派 | 锁屏、亮屏不表示能解开密码；各私有入口可用性不同 |
| 图片算法 | Go 图像处理与原生识别结合 | `images` 复用 `sdk/imaging`，`opencv` 适配 | 相似函数名不证明算法、阈值及返回值完全一致 |
| OCR | libppocr → NCNN + OpenCV | `ppocr` + `host/ios/objc/ag_rec.mm` | 构建包仍引用参考资源预编译库；模型、C++ ABI、生命周期待验收 |
| YOLO / 点阵识别 | libyolo / libdotocr、NCNN/OpenCV | `yolo/dotocr` + 同一识别适配器 | 有胶水不等于模型加载和识别结果已验证 |
| 悬浮层 | cimgui、Metal、大量 Go 绑定 | `hud/console`、自编 libimgui、部分 Go imgui 包装 | 底层版本相同不等于 Go API 覆盖相同；ScriptUI 尚未对应 |
| Go 脚本 | 内置 Yaegi、SDK 类型/函数符号绑定 | `script` 使用上游 Yaegi v0.16.3 | 本轮补类型与构造器、运行隔离、工程与 embed；并非 JS 工作流引擎 |
| 文件/存储/HTTP | Go 标准库与 SDK 包 | `files/storages/https` | 路径、权限、错误和并发语义须分别验证 |
| 外壳通信/穿透 | appctl、frpc、不同本地端口；其他网络库符号 | `apkctl/tunnel`、本地 HTTP | 端口相同不等于协议相同；依赖符号不能证明 WS/MQTT/QUIC 都是启用功能 |
| SDK/脚本更新 | 可见远端下载、任务/缓存/加载相关代码 | 脚本远程缓存、`sdk.install` | 不足以证明签名验证、原子切换、健康检查及失败回滚完成 |
| 云端 Agent | 不是仅凭此 SDK 样本可判定的部分 | 现有 v2 Agent 仍直接依赖安卓能力 | iOS SDK 尚未装配到云控 Agent，不能把 SDK 编译当作云控接入成功 |

## 4. 控件树：本次核实到的真实调用链

### 4.1 AutoGo 的采集路径

```text
uiacc.New
  → loopAcquire（引用计数）
  → AXBootstrap
  → 后台 sweepOnce
      → AXSweepNodesJSON
      → JSON 解码
      → 原子替换共享节点快照
      → 等待约 100 ms 后再采集
  → Find / FindOnce / Wait 从共享快照筛选
  → 最后一次 Release 停止后台循环
```

原生采集并非 macOS 风格 `AXUIElementCopyAttributeValue(..., "AXTitle", ...)`：

1. 加载 AXRuntime、AccessibilityUI/Utilities 与 libAccessibility。
2. 使用 `AXUIElement` 的 `uiApplicationAtCoordinate:` 获取应用，按 pid 归并。
3. `enableCache:` **传 NSNumber**，再 `updateCacheWithAttributes:`、`cachedAttributes`。
4. 以属性 **5039** 获取快照；递归读取 `UIAccessibilitySnapshotKeyChildren`、`UIAccessibilitySnapshotKeyAttributes`。
5. 用 `uiElementAtCoordinate:` 按坐标补采可见节点。
6. 节点去重、排序、转 JSON；像素坐标由点坐标乘屏幕 scale。

从缓存数组与节点构造数据流核实的映射：

| 属性 | 语义 |
| --- | --- |
| 2003 | 几何矩形 |
| 2001 | label → Text |
| 2006 | value → Desc |
| 5003 | role |
| 5004 | class2，类名优先来源 |
| 3003 | bundle |
| 5019 | identifier |
| 2004 | traits 位集合 |
| 5034 | enabled |
| 5039 | 应用控件快照 |

`bpx/bpy` 是节点相对父节点的偏移，不能填 0；`indexInParent` 是父节点内的索引，不能用整个遍历队列的序号冒充。

### 4.2 我们此前存在的问题及本轮处理

- 原来的两个原生文件维护了两套 CF 风格采集，不能与上述对象快照路径互换：本轮收敛为 `ax_runtime_ios.m`，`ag_ax.m` 与 `uiacc_ios.m` 仅包装同一采集器。
- 描述、类名、包名、父坐标、索引与 enabled 来源不准：按已核实数据源归一化；未取得布尔状态时不再当成 false 去匹配。
- 每次查询直接重采集：改为进程内共享刷新，循环在每次采集后等待 100 ms；采集失败清除旧快照，长期不更新的快照报超时。
- Release 只置标记、等待可能不退出：增加引用计数、取消和错误入口，支持 `FindContext`、`WaitForContext`、`Error`。
- 脚本忘记 Release：解释器内 `uiacc.New` 绑定到本次任务 context，任务结束自动释放引用。

### 4.3 仍需明确的差异

样本还会处理 `_AXSFullKeyboardAccessSetEnabled`、CFPreferences 以及临时切换有效用户/组。它们是运行环境初始化的一部分，不是“加载框架就获得所有权限”。本轮没有自动修改设备全局辅助功能设置或进程身份；若 AX 没准备好，会返回失败。后续应在目标设备验证需要哪些初始化，再提供显式准备与恢复流程。

我们的网格密度、排序补项、公共 traits 推断与它并非逐条相同；未还原的私有 traits 不猜编号。坐标补采得不到可靠父子关系的节点将索引标为 -1。横竖屏、WebView、键盘、系统弹窗及密码输入控件仍需真机验收。

C ABI `AXSweepNodesJSON` 当前输出我方标准字段；不承诺对闭源程序的 JSON 格式二进制兼容。

可复核的样本地址（同一 SHA256）：`AXBootstrap 0x1010ba3e8`、`AXSweepNodesJSON 0x1010ba494`、快照缓存 `0x1010bc30c`、递归展开 `0x1010bcaf4`、属性缓存 `0x1010bcfcc`、节点构造 `0x1010bd7c8`、矩形解析 `0x1010bec78`。

## 5. 脚本解释器：相同方向与实际差异

### 5.1 AutoGo 已核实的行为

`main.runGoFile` 和 `main.runZip` 都可见创建新解释器、合并符号、`Interpreter.Use`、`EvalPath` 的调用。工程加载有模块/根目录处理、ZIP 读取、多文件和资源预处理；`PreprocessSource` 与 embed 辅助逻辑支持 string、[]byte、文件系统资源。

符号表不止函数：

- uiacc：`New`、`Uiacc`、`UiObject`、`Rect`。
- ppocr：`New`、`Ppocr`、`Result`。
- images：图像函数与 `Point`。
- imgui：大量类型、常量、函数与结构体绑定。

`app/yaegi/interp` 表明内置了一份解释器源码；单凭包路径和字符串数量不能证明改过哪些逻辑。样本中同样有 `EvalWithContext`、`EvalPathWithContext`，但上述两个主入口调用的是 `EvalPath`，不能据此说它们已做了强制超时。

### 5.2 本轮我们的修正

- 自动生成公开函数、类型、常量、变量；不再因为函数返回指针/结构体就跳过。接收者方法随反射类型可用。
- 当前注册 **22 个能力包、253 个符号**；这是覆盖检查数据，不是“253 个功能已实测”。不包含解释器自身和平台自检包。
- 每次 Run 创建新解释器；同一个 Runner 串行，不同 Runner 的参数、日志和结果使用独立 Session。
- `Use` 错误向上返回；stdout/stderr 与 api.Log 都归入本次结果，参数/日志/结果有大小限制。
- 使用 `EvalWithContext` / `EvalPathWithContext`；超时后不会复用解释器。已进入的 C/原生调用不能被 Go context 强制打断，需要任务进程看护兜底。
- 新增 `RunFile`、`RunProject`、`RunZip`，支持工程本地 import、多 Go 文件与只读 embed 资源。
- ZIP 与资源限制：最多 1024 个文件、总量 32 MiB；拒绝越界/重复 ZIP 路径、符号链接和不存在的 embed 资源；单个 Go 源文件最多 4 MiB。
- `Run` 单字符串遇到 go:embed 明确拒绝，避免资源静默变成空值。

这不是安全沙箱。标准库和设备能力被授权给可信脚本；不应据此直接开放任意用户源码。原生能力、触摸硬件和应用进程仍为设备共享资源，独立 Session 只隔离任务数据，不提供多任务并行控制同一设备的授权。

这些新增入口属于 SDK Go API；当前壳的 script.run 与 Runtime/task 仍以源码字符串执行，尚未自动接入 ZIP 上传/文件工程分派。

源码文件系统仅服务解释器和 embed；不会改变整个进程工作目录，也不把 ZIP 中资源自动铺到真实文件系统。普通 `os.ReadFile` 需要实际文件路径。这与样本会落盘部分资源的行为不同，应按实际业务需要再扩展。

## 6. 视频与识别不能仅看“有接口”

### 视频

样本有 `BridgeH264SetCallback` → C 回调 → Go `goH264DataCallback` 的数据链；原始帧也有独立回调。我们 Go `video.Start/Read/Close` 走原生队列，是另一种有效的接口设计，但不等于 C Bridge 已完成。

已发现且本轮没有改动的 C Bridge 缺陷：

1. `ag_bridge.m` 的 `AGBridgeCallJSON` 没有 `video.*` 分派。
2. `gH264Callback/gRawFrameCallback` 只保存，没有发送帧的调用点。
3. `BridgeH264SetEncoderParams` 我方 4 参数；样本调用点是 3 参数（resolution、bitrate、fps）。
4. `BridgeRawFrameStart` 我方带 fps；样本相应入口无参。
5. `video.start` 底层需要 token 与 config，C 包装没有提供；仅补一个前缀分派不足以修好。

如果以后使用我方 Go 拉取 API，可以不追求闭源 C ABI 兼容；如果承诺兼容，必须先确定回调签名、线程、内存所有权与停止排空行为，不能留下“设置成功但永不回调”的桥。

### 识别和悬浮层

识别适配器引用了参考资源的 ppocr/ncnn/opencv/yolo/dotocr 预编译库，不能写成识别引擎完全自主实现。应继续核对来源、分发条件、可重建性、模型和 OpenCV C++ ABI。

libimgui 版本/导出相似只说明底层接近，不能证明我们少量 Go 包装覆盖它大量脚本绑定。HUD、console、ImGui 和 ScriptUI 是不同层次，分别验收。

## 7. 其他需要继续处理的真实代码问题

| 优先级 | 位置 | 具体问题/下一步 |
| --- | --- | --- |
| P1 | `internal/native/ag_bridge.m` | C 逐指 TouchDown/Move 只传一根手指的完整帧，Up 传空帧，会覆盖或释放其他手指；与 Go 手指状态路径不一致 |
| P1 | `motion/touch.go` | 简版像素坐标转换用 sync.Once 缓存尺寸；首次读取失败后不能恢复，方向切换也不能刷新；多次调用组合动作的串行性需统一 |
| P1 | `host/ios/Sources/AGRootViewController.m` | 多个按钮同步调用 SDK；若任务需要回到 UIKit 主线程，会阻塞 UI，主线程边界可能超时；需异步按钮执行与状态反馈 |
| P1 | `script/remote.go` | 短 SHA 会触发切片异常；下载 context 未实际传入 HTTP；先覆盖缓存再执行不能称为最后可用版本；下载无本轮工程相同的大小边界 |
| P1 | `cmd/ioshost` 的 sdk.install | 仅长度检查后写固定文件，缺少 hash/签名/架构校验、原子发布、已知可用版本、重启后健康检查与回滚 |
| P1 | `cmd/runtimeapp` / 宿主任务管理 | `/task` 使用 Background，需核实断开、超时、停止和原生阻塞时能否真正终止并回收子进程，避免只返回失败仍继续点手机 |
| P2 | `selftest/selftest.go` | 非特定“未实现”字符串的错误多数归为 env，包括参数错误；统计会掩盖真实实现问题，且探针不是无副作用 |
| P2 | `apkctl/tunnel` | 同端口/函数名不能证明与参考通信协议兼容；进程退出、重连、鉴权需要逐条确认 |
| P2 | 两个 SDK 源码目录 | 基线已有多处不同，必须建立来源和同步约定，避免两套不同 bug |
| 后续接入 | `v2/agent`、`v2/protocol/messages.go` | Agent 仍直接引入安卓；设备注册仍有 androidVer，尚不能据此完成 iOS/Android 的平台识别与能力装配 |

这些是已定位到的代码问题，不是承诺本轮已全部修复。当前用户已授权的上一项修复重点为 AX 与解释器；其他项在此形成后续改动清单，避免同时改动整个 Agent 框架。

## 8. 本轮验证与下一步验收

本轮 Windows / Go 1.25.1，`GOWORK=off`。已验证：

- 主模块与独立镜像的脚本、选择器及生成器相关 Go 测试。
- 解释器实际使用 uiacc/ppocr 构造器、公开结构体和方法。
- 重复运行不继承脚本全局变量、并发任务参数/日志/结果不串线、纯 Go 死循环取消及后续重用 Runner。
- 工程多文件、本地 import、ZIP 公共外层目录、embed string/[]byte/FS 和无效路径拒绝。
- 选择器共享首帧、最后引用停止、错误清除旧节点、未知布尔值不误匹配、释放后结束等待。

本机不能用 Apple SDK 编译新增 Objective-C；不把本次 Go 成功说成 iOS 原生通过。仓库中既有 Apple CI / 模拟器结果属于此前版本记录，不能自动覆盖本次变更。尤其“missing=0”只表示探针没落入该分类，不表示点击已生效；历史计数 0→0 也不是注入成功。

下一次真机验收至少包括：

1. 两个 App + SpringBoard + 系统弹窗 + 键盘的 AX 树，核对文字/值/包名/父坐标/scale。
2. 多选择器共享、取消、重复创建/释放、CPU 与长时间内存变化。
3. 横竖屏点击、双指独立抬起、取消后抬手与中文输入。
4. Go 视频路径持续取帧、关键帧、PTS、降码率、停止释放；C 回调桥修复后单独验收。
5. OCR/YOLO 真实输入、结果正确性与重复关闭。
6. 新旧 SDK 更新成功、坏包拒绝、启动失败回退，以及任务进程被终止后无残留动作。

参考路线可沿用，验收仍按“构建 → ABI/加载 → 功能 → 业务效果”逐层完成。
