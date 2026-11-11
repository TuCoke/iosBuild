# iOS SDK 对标 AutoGo：当前进度与未完成项

更新：2026-11-11。本文是当前状态入口；较早分析中的“完全对标”“全部可用”或“截图/AX 尚未实现”不能继续作为当前结论。

## 先看结论

核心设备能力、Go 脚本解释与独立执行进程已有实现；SDK 原生截图、AX 节点、宿主 JavaScriptCore 基础通道已在模拟器验证。**目前还没有完整达到 AutoGo iOS 的全部能力与运行行为。**

明确的代码缺项集中在完整 OpenCV Go 绑定、完整 ImGui Go 绑定、跨 App 全局悬浮宿主、JavaScriptCore 原生 UI 桥，以及依赖制品和更新恢复闭环。视频、输入、识别等已有代码的功能，仍有未完成的验收；不能把它们统一写成“没有实现”。

本轮只整理状态、修正文档及说明性注释，不新增设备功能，不修改云控 Agent、MCP 或服务器工作流。

主仓库实现快照：[891116e](https://gitee.com/Lovely_Rabbit/agent-cgo/commit/891116e9109d658ef584a130d1bc19f7582b205c)，已推送 `v2` 分支。该提交包含此前完成的 iOS 能力与独立宿主改动；后续文档提交只更新当前对标状态，不将所有缺项写成已完成。

## 判定方式与证据

| 标记 | 含义 |
| --- | --- |
| 已实现、已验证 | 已有对应代码，并且在表内所述范围实际验证 |
| 已实现、待验收 | 有具体实现，但运行效果或真机授权尚未证明 |
| 部分实现 | 基础可用，但仍缺明确功能、装配或生命周期环节 |
| 未实现 | 当前源码没有提供对应的完整模块或实现 |
| 待核对差异 | 两侧实现不同，不能在未核实行为前宣称完全一致 |

参考核心是提供的 `AutoGo-ios/resources/libs/ios/libgoeval.dylib`，SHA256：`b4e157737a9bba0de49fd98ad5d9b5e30519118cacbd63df46e807b0e6f5af48`。依据包括其 Go 编译符号、ObjC 方法、调用参数、宿主程序和配套资源；没有在相同真机上把闭源程序与本项目逐项做黑盒对照。

该文件虽然名为 dylib，实际类型是 `MH_EXECUTE`。我方是 `libagovios.dylib` 动态 SDK 加 `Runtime/app` 薄执行体；两侧不能作为同形制品直接互换，也不能继承对方的真机测试结论。

当前验证依据：

- [修复版 9a1900d](https://github.com/TuCoke/iosBuild/actions/runs/42987054926)：Go 检查、真实 iPhoneOS/CGo 编译、IPA/DEB 打包、模拟器功能验证四项通过。
- [收尾版 70e5445](https://github.com/TuCoke/iosBuild/actions/runs/42988971066)：四项通过。首次模拟器启动因 FrontBoard 尚未注册应用失败，单独重跑通过，未放宽 SDK 断言。
- 原生 JPEG 已逐张查看，画面与方向正常；不是用外部 `simctl` 截图替代 SDK 采集。
- 验证设备是 GitHub 的 iPhone 16 Pro / iOS 18.5 / arm64 模拟器，**不是实体 iPhone**。本次文档更新未重复运行编译或设备测试。

## 完整能力对照

下表的源码位置以主仓库为准，目录前缀统一为 `v2/sdk/ios/`；宿主例外单独写明。

| 能力 | AutoGo 样本的技术路线或结构 | 我们当前状态与实现位置 | 还缺什么或未验证什么 |
| --- | --- | --- | --- |
| 点击、长按、滑动 | IOHID 父 digitizer 事件和手指子事件 | 已实现：`motion/`、`internal/native/touch_ios.m`；模拟器目标块点击计数实际 0→1 | 长按、轨迹、不同 App 的实际响应仍需真机验收 |
| 多指、双指缩放 | 多个手指事件组合与时序 | 已实现：完整帧、增量触点、Gesture/Pinch、取消抬手 | 多指交接、边缘坐标、复杂手势没有完整真机验收；不能声称事件字节完全相同 |
| 坐标与方向 | 屏幕自然尺寸、scale、FrontBoard 方向 | 已实现：`internal/native/display_ios.m`、`types/` | 横竖屏切换、iPad、显示缩放与多机型仍需验收 |
| 中文与键盘 | Unicode 组合字符分段、HID 按键 | 已实现：`ime/`、`internal/native/touch_ios.m` | 中文组合、emoji、各 App 输入框和系统授权需真机验收 |
| 截图 | CARenderServer → IOSurface → CGImage | 已实现、模拟器已验证：`capture/`、`internal/native/capture_ios.m` | 真机、受保护画面、方向切换仍待验收；私有函数返回约定不能跨目标照搬 |
| H.264 视频 | VideoToolbox、SPS/PPS、Annex B、编码回调 | 已实现、已编译：`video/`、`internal/native/video_ios.m` | 尚缺真实连续编码、关键帧、调码率、播放与长时间负载验收；截图通过不等于视频通过 |
| 原始帧与 C→Go 回调 | BGRA 帧和编码数据回调 | 已实现：`streambridge_ios.m`、`callbacks_ios.go`、`video/callbacks.go` | 尚缺连续帧、背压、停止/重启及长时间资源回收实测 |
| AX 控件采集 | AXRuntime / AXUIElement 缓存、5039 快照、坐标补采 | 已实现、模拟器已验证：`internal/native/ax_runtime_ios.m`、`uiacc/` | 参考测试中宿主 79 / 独立进程 80 个真实节点；跨 App、系统弹窗及不同真机版本仍待验收 |
| 节点查询与等待 | 节点属性筛选、刷新与生命周期 | 已实现：`uiacc/selector.go`、`uiacc/cache.go` | 有 Go 逻辑验证，未做与样本控件树逐字段、逐页面的黑盒等价对照 |
| AX 环境初始化 | 完整键盘控制、坐标预热、必要的身份处理 | 部分实现：`ax_runtime_ios.m`、`uiacc/environment.go` | 样本中心预热后关闭设置；我方保持开启并提供显式恢复；预热顺序和 root/mobile 启动环境仍有差异 |
| 应用管理 | SpringBoard / FrontBoard / LSApplicationWorkspace，安装 helper | 已实现：`app/`、`internal/native/app_ios.m`、`installer_ios.m` | 启动、前台识别、图标、安装/卸载/清数据尚未完整真机验收 |
| 设备与系统 | 系统信息、亮度、剪贴板、锁屏、网络等 | 已实现：`device/`、`system/`、`internal/native/device_ios.m` | 多项依赖系统授权；自检受限不能直接认定源码缺失 |
| 基础图像处理与找色 | Go imaging / Go 图像计算 | 已实现：`images/`；缩放、旋转、透明度灰度、阈值等有 Go 验证 | 找色尚未与闭源样本逐像素对照；不等于完整 OpenCV 接口 |
| 完整 OpenCV Go 能力 | Mat、图像匹配及更多 OpenCV 公开绑定 | **未实现**：`opencv/` 当前只有说明文件 | 缺 Go 类型、资源释放、Mat/FindImage 等完整公开绑定；链接 OpenCV 原生库不能补上这一层 |
| PPOCR / YOLO / 点阵 OCR | NCNN、OpenCV 与识别引擎 dylib | 已实现适配：`ppocr/`、`yolo/`、`dotocr/`，胶水在 `v2/ios/host/ios/objc/ag_rec.mm` | 缺实际模型识别验收；原生引擎仍依赖预编译库，未做到算法全部自建源码；资源交付见下文 |
| 基础 HUD / Console | ImGui + Metal | 部分实现：`hud/`、`console/`、`imgui/` 及宿主 `imgui_overlay.mm` | 能提供基础面板；不等于跨 App 全局悬浮或完整脚本 UI |
| 完整 ImGui Go 绑定 | 大量 cimgui 函数、控件与结构访问器 | **仅少量绑定**：`imgui/cimgui.go`，当前 12 个基础函数 | 缺 Button、输入框、表格等大量 Go 控件接口、结构封装、生成器与完整脚本绑定；原生 cimgui 编译成功不等于 Go API 完成 |
| 全局悬浮窗 / 悬浮球 | agoverlayd / floatball 等独立宿主、全局窗口与触摸通道 | **未实现该完整宿主**；当前面板依附自身 UIWindowScene | 缺独立全局宿主、触摸 socket、跨 App 显示、进程看护和退出清理 |
| JavaScriptCore 基础控制 | 宿主 JS 引擎、TCP 8990 与消息回调 | 已实现、基础链路已验证：`apkctl/`、宿主 `AGAppCtlServer.m`；实际 `1+2=3` | 基础 Eval/消息不是完整原生 UI 桥 |
| JS 原生 UI 桥 | AGJSSelector / AGJSStruct / AGJSActionTarget 等 | **部分基础通道，完整 UI 桥未实现** | 缺对应原生对象/结构映射、UI 构建、动作回调及完整双向应答装配 |
| Go 脚本解释 | 内嵌 app/yaegi、SDK 及其他包绑定 | 已实现：上游 Yaegi、`script/`、`script/api/`；支持 Go/工程/ZIP/import/embed | 样本自有 fork 的差异、core 和全部第三方绑定没有完整核实；不能保证任意 AutoGo 脚本原样运行 |
| 独立脚本进程 | 独立任务执行体及进程管理 | 已实现、正常执行已验证：`script/worker/`、宿主 `RuntimeMain.m` | 已有超时、父进程看护和 waitpid 代码；杀宿主、阻塞原生调用等故障注入未完整验收 |
| 文件、Plist、存储、HTTP | 文件 API、属性列表、SQLite、HTTP | 已实现：`files/`、`storages/`、`https/`，已有相关 Go 验证 | Plist 使用公开 howett.net/plist；并未证明与样本内部 fork 所有边界完全一致 |
| SDK 更新 | 安装候选版本、启动加载及进程生命周期 | 基础已实现：`update/`、宿主 `AGSDKLoader.m` | 当前为安装后重启生效；完整平台校验、依赖/资源清单、版本健康检查与自动回滚闭环仍缺；真实更新尚未验收 |
| 穿透 | frpc 客户端及生命周期 | 已实现：`tunnel/`，有包内二进制定位及源码构建回退 | 真实配置、外网连通和长期保活尚未验收；缺参数时启动失败不算“没有实现” |

特别区分：`device.wake` 与 `dotocr.set_dict` 已有原生分支。模拟器缺少系统入口或未加载识别库时返回 unsupported，不等于没有源码。9a1900d 的 70 项探针中，40 项为调用返回成功、4 项环境失败、2 项不支持、6 项跳过有副作用、18 项仅参数拒绝；这些数字不能换算成“70 项设备能力全部通过”。

## 真正还需要写代码的地方

| 缺项 | 对用户有什么影响 | 后续实现位置 |
| --- | --- | --- |
| 完整 OpenCV Go 绑定 | 无法直接使用完整 Mat/图像匹配等 API | `v2/sdk/ios/opencv/`，同步构建镜像 |
| 完整 ImGui Go 绑定和生成器 | 无法用 Go 脚本完整构建按钮、表单等 UI | `v2/sdk/ios/imgui/`，生成工具与 `script/api/` |
| 全局悬浮宿主 | 切到其他 App 后，不能把当前 WindowScene 面板当作全局控制层 | `v2/ios/host/ios/` 的独立宿主、事件通道与生命周期 |
| JS 原生 UI 桥 | JavaScriptCore 能执行 JS，但不能等价调用样本全部原生 UI 能力 | 宿主 `AGAppCtlServer.m` 及对应桥接类 |
| 原生依赖的统一交付 | 主仓库干净检出与验证仓预置资源不一致，可能生成没有识别库的包 | 原生资源清单、获取/校验流程、构建脚本与缺件检查 |
| 完整更新与恢复闭环 | 新版 SDK 可安装，不代表平台错误或启动后异常能自动恢复 | `update/`、`AGSDKLoader.m` 和宿主更新装配 |

另外有两项需要核对后再决定具体实现，不能直接写成已经完成：

1. **AX 初始化与身份顺序。** 已确认设置恢复和中心预热存在差异；root 子进程启动路径也不能仅凭 entitlements 判定实际生效。需要在已授权真机上核对行为后补齐，不在并发 Go 进程里随意切换整进程身份。
2. **Yaegi fork 与脚本兼容层。** 样本有自有解释器路径，我方用上游 v0.16.3；还缺完整差异清单。样本 `core` 的部分能力已分散到我方 app/device/motion/ime/video/utils；缺同路径包装不等于这些设备能力都没有。若要求原样运行样本脚本，还需导入路径、类型、常量、签名和行为兼容。

## 原生依赖交付的具体缺口

已检查两侧 Git 跟踪文件：

- GitHub `iosBuild` 验证仓库预置了 `host/ios/Runtime/libppocr.dylib`、`libyolo.dylib`、`libdotocr.dylib`、`libncnn.dylib` 和三个 OpenCV dylib，以及 `Frameworks/frpc-ios`。
- 主仓库的 `v2/ios/host/ios/Runtime/` 没有这些文件；`Frameworks/` 当前只有说明文件。frpc 有从源码构建的回退，识别运行库没有同等的统一获取闭环。
- 当前工作流逐个复制已经存在的 Runtime dylib，没有因识别库缺件而强制失败。因此 **IPA 打包通过不保证 OCR/YOLO/点阵识别在制品中可用**。

后续需要明确库/模型版本、架构和系统平台、来源、摘要及包内路径；制作可重复获取的制品，并在要求启用识别的发布包中检查缺件。识别库没有全部可重建源码、模型效果未经验证，分别登记，不能用 Go 包目录存在替代完成状态。

## 热更新已经有什么，还没有什么

已有：Mach-O/arm64/dylib 检查、可选 SHA256、临时文件写入和同步、原子替换、真实加载路径与加载时摘要、候选版本状态、重启生效；启动器在候选库加载失败时还会尝试包内库。

缺少的完整闭环包括：

- 校验 `LC_BUILD_VERSION` 等平台信息，防止仅凭 arm64 把模拟器库当成真机库。
- SDK 与原生依赖/模型的版本清单、兼容关系和发布校验；摘要不等于系统签名授权。
- 启动后健康检查、已知可用版本保存、异常后的选择/恢复和可观测升级结果。
- 真机上执行“安装 → 重启 → 加载新版本 → 故障恢复”的完整验收。

它不是在新线程中替换当前正在运行的 Go 动态库。独立线程/进程用于任务执行与看护；当前 SDK 更新需要重启宿主后生效。

## 还存在的版本与行为差异

| 项目 | 当前差异 | 判定 |
| --- | --- | --- |
| 最低系统版本 | 样本元数据最低 iOS 13；我方构建目标 iOS 15 | 不表示我们已经支持 13/14；需要实际构建、依赖与设备覆盖，不能只改版本字符串 |
| ImGui 版本 | 样本记录有 1.92.5 WIP / 19244，当前 vendor 为 1.92.5 / 19250 | 属于版本差异，不能仅凭“1.92.5”宣称完全相同 |
| 图像算法 | 多项算法已核实并有实现；找色仍复用公共实现 | 尚未与样本逐像素黑盒对照 |
| 缓冲所有权 | 我方将帧复制为 Go 可持有的独立内存 | 有意的生命周期约定，不要求把它改成借用缓冲才算实现 |
| 调用与结果协议 | 我方有 context、错误分类及自定义脚本参数/结果协议 | 不能宣称与样本二进制或宿主协议全部互换 |

不使用函数数量计算“完成百分比”。现有 [函数名清单](ios-autogo-function-inventory.json) 只比较编译名称，不覆盖参数、类型、常量、行为、授权和稳定性。

## 建议的后续顺序

1. 先补发布依赖清单、缺件检查和更新恢复闭环，让同一份源码与制品可以复现。
2. 按实际脚本需要补 OpenCV 图像匹配与类型/资源管理。
3. 按是否需要手机端脚本 UI，补完整 ImGui、全局悬浮宿主和 JS 原生 UI 桥。
4. 单独推进真机验收：输入/多指/Unicode、截图与方向、AX 跨 App、连续视频、识别、安装与更新恢复。

这四类工作应分别记录。代码缺项靠实现补齐；真机效果靠实际验收确认；已知的接口组织差异不应盲目转化为新目录或整框架重构。

## 相关记录

- [原生能力、参数与 ABI 对齐](ios-autogo-sdk-alignment.md)
- [脚本、存储、宿主与执行体对齐](ios-autogo-runtime-alignment.md)
- [截图与 AX 失败定位及实测](ios-capture-ax-diagnostics.md)

主实现位置为 `v2/sdk/ios/`；`v2/ios/sdk/ios/` 是独立 Apple 构建镜像。新增功能应保持同一实现来源和明确同步，不在 Agent、MCP 和 SDK 各写一份设备算法。
