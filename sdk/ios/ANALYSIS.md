# AutoGo iOS 参考依据

本记录区分参考包中的静态证据与我们的实现，不把反汇编结果等同于真机验证。

2026-11-11 已继续核实 AX 属性映射和 Yaegi 入口，早期“未采集/未接入”的描述属于历史状态。当前结论及未完成项见 [完整核查](../../docs/references/memory/ios-autogo-stack-audit.md)。

## 实际检查的输入

- `AutoGo-ios/resources/libs/ios/libgoeval.dylib`，41,672,232 字节。
- 上述文件本轮 SHA256：`b4e157737a9bba0de49fd98ad5d9b5e30519118cacbd63df46e807b0e6f5af48`。
- 同目录中的 OpenCV、NCNN、PPOCR、YOLO 等原生库。
- `AutoGo-ios/resources/META-INF/ios.toml`；其中是应用包名、名称、自动运行等打包设置，不是系统授权配置。

`libgoeval.dylib` 的 Mach-O filetype 是 `MH_EXECUTE`，不能因为扩展名就把它当成我们可直接链接的动态库。当前 SDK 不加载此文件。

## 已观察的结构

二进制包含 `github.com/Dasongzi1366/AutoGo/motion`、`ime`、`images`、`uiacc` 等 Go 包，以及 Go 到原生桥的调用。还包含 Yaegi 相关代码与 SDK 符号绑定。脚本解释器与设备能力是两层，我们只借鉴能力边界。

视频部分存在实际实现及以下调用：

| 接口 | 观察结果 |
| --- | --- |
| `core.H264StreamSetCallback` | 调用 `_Cfunc_BridgeH264SetCallback` |
| `core.goH264DataCallback` | 复制原生数据为 Go 字节并调用已登记函数 |
| `core.SetEncoderParams` | 对应 `BridgeH264SetEncoderParams` |
| `core.StartEncoding` / `StopEncoding` | 对应原生编码启动、停止 |
| `core.RawFrameStart` / `goRawFrameCallback` | 原始帧启动与回调 |
| VideoToolbox 导入 | 创建、准备、编码、排空及销毁压缩会话 |
| H.264 参数设置 | 实时、帧重排、码率、预期帧率、关键帧间隔、Profile |
| 编码日志 | 原始尺寸、maxSize、bitrate、fps、旋转信息、停止帧数 |

这能证明具有连续采集和 H.264 数据输出，不能单独证明最终网络传输是 WebRTC 或 WebSocket。

输入相关存在手指编号桥接、父数字化仪事件和子手指事件、键盘及 Unicode 事件。Unicode 调用点表现为 allocator、时间戳、UTF-8 数据、字节长度、编码标志、options 六参数；这是私有 ABI，仍需目标系统验证。

控件部分存在 `AXBootstrap`、`AXSweepNodesJSON` 与 Accessibility 私有框架引用，但当前没有足够可靠的属性编号和线程约定。没有据此猜测并发布一个声称可用的 AX 原生采集器；`uiacc` 仅提供可接入真实采集器的查询能力。

## 我们的独立实现选择

- 原生层动态解析系统入口，缺失时明确报错，不将符号存在当作系统授权。
- Go API 采用 context、归一化输入坐标和可分类错误，不照搬参考包的静默失败。
- 视频由原生有界队列交给 Go 拉取，避免原生编码线程执行用户回调。
- 单个进程集中管理输入状态和视频句柄；原生视频 token 不替代云端租户和 Session。
- 未复制参考包的闭源实现、预编译库、授权文件或提权工具。

## 可核对的外部资料

- [AutoGo iOS 动作文档](https://autogo.cc/#/ios/motion)
- [AutoGo iOS 控件文档](https://autogo.cc/#/ios/uiacc)
- [WebKit iOS IOKit SPI 声明](https://github.com/WebKit/WebKit/blob/main/Source/WebCore/PAL/pal/spi/ios/IOKitSPIIOS.h)
- [Apple VideoToolbox](https://developer.apple.com/documentation/videotoolbox)
- [TrollStore 官方环境说明](https://github.com/opa334/TrollStore)

## 验证记录

第一轮实现只做源码格式化、静态阅读与文件范围检查，没有执行编译或测试。

2026-11-09 用户要求确认能否编译后，执行以下验证：

| 验证 | 结果 | 结论边界 |
| --- | --- | --- |
| Windows/amd64、CGO_ENABLED=0，`GOWORK=off go build ./sdk/ios/...` | 通过 | 只编译了 Go 代码和非 iOS stub，未编译 Objective-C |
| 同一环境，`GOWORK=off go test ./sdk/ios/...` | 通过 | 新增十个顶层测试，覆盖坐标、尺寸、输入闸门取消、参数拦截、非 iOS 明确失败、控件树循环及等待 |
| iOS/arm64、CGO_ENABLED=1，`go list -e -json ./sdk/ios/internal/native` | 选中了 `bridge_ios.go` 和六个 `.m` 文件，忽略 stub | 仅确认构建文件选择，不是编译或链接成功 |
| 同一 iOS 目标，`go build ./sdk/ios/...` | 失败：`cgo: C compiler "clang" not found` | 在 runtime/cgo 工具链阶段失败，还没有验证原生源码是否可编译 |

运行环境为 Windows、Go 1.25.1，本机未发现 Clang、xcrun 或 Xcode。以上构建命令在 `v2/` 下运行，并在命令进程中设置 `GOWORK=off`，未修改仓库根目录的工作区文件。

### 2026-11-09 真 Apple SDK 编译（借用一台 Mac）

在 macOS 15.7.7 / arm64 / Xcode 26.3 / iPhoneOS SDK 26.2 上，用 `xcrun --sdk iphoneos clang -c -arch arm64 -mios-version-min=14.0 -isysroot <SDK> -fno-objc-arc` 逐个编译六个 `.m`：

| 验证 | 结果 |
| --- | --- |
| `app_ios.m` `device_ios.m` `runtime_ios.m` `touch_ios.m` `video_ios.m` | 通过 |
| `capture_ios.m`（首轮） | **失败**：`'IOSurface/IOSurface.h' file not found` |
| `capture_ios.m`（修正后） | 通过 |
| `GOOS=ios CGO_ENABLED=1 go build ./...` | 未执行（该 Mac 未安装 Go） |

首轮失败的原因值得记录：**IOSurface 在 iOS 上属于私有框架，iPhoneOS SDK 里没有公开头文件**（`IOSurface.h` 只随 macOS SDK 发布）。在 Windows 上用桩头文件做语法检查时桩里有这个头，所以查不出这个问题。

修正方式与 IOHIDEvent 一致：不 import 私有头，本地声明类型，函数用 `dlsym` 解析（`AGSymbol`），字典键改用同名 `CFSTR` 字面量（`CFDictionary` 按 CFString 值比较，与私有常量等价）。这样既不依赖头文件也不依赖链接期私有框架。

**结论边界**：这证明六个 `.m` 能被真实的 iPhoneOS 26.2 SDK 编译，不证明链接、签名、安装或运行行为；`GOOS=ios` 的 cgo 构建仍未验证（缺 Go 工具链）。

没有执行安装、签名、部署或真机操作。私有 API ABI、权限、横竖屏、多指、文字输入和视频效果仍待 Apple 工具链与真机验收。Go 测试通过不能替代这些验收。

### 2026-11-09 全链路打通（GitHub macOS CI）与平台边界结论

在 GitHub Actions（`macos-15`，Apple Silicon，自带完整 Xcode）上把整条链路跑通了，代码在小仓库 `iosBuild`（对应本目录的镜像）：

| 步骤 | 结果 |
| --- | --- |
| 用真 iPhoneOS SDK 编译 6 个 `.m`（`-fno-objc-arc`） | 通过，产物是 arm64 `MH_OBJECT` |
| `GOOS=ios GOARCH=arm64 CGO_ENABLED=1 go build ./sdk/ios/...` | 通过（修了 cgo 传 `void*` 需显式 `unsafe.Pointer`） |
| `cmd/ioshost` 编成 c-archive（`libagovios.a` + `.h`） | 通过 |
| `xcodegen generate` + `xcodebuild -sdk iphoneos CODE_SIGNING_ALLOWED=NO` | 通过，产出未签名 `.app` |
| 打包 `Payload/` → `.ipa` | 通过，产物约 950 KB |

产物核验（下载 artifact 后本地检查）：主二进制为 arm64 `MH_EXECUTE` 2.5 MB，内含 `agov_ios_invoke`、`autogov1/v2/sdk/ios/motion`、`IOHIDEventSystemClientCreate`、`CARenderServerRenderDisplay`、`runtime.capabilities` 与 Go 运行时标记，说明 Go + cgo + 六个 `.m` 确实链接进了 App。无 `_CodeSignature`、无 `embedded.mobileprovision`，符合"未签名"预期。

**平台边界结论（本次确定）**：目标设备是 **iPhone / iOS 18.7，未越狱，也没有 TrollStore**（TrollStore 上限 iOS 17.0）。这套 SDK 依赖 `IOHIDEventSystemClientDispatchEvent` 注入与 `CARenderServerRenderDisplay` 采集，二者都需要系统级私有 entitlement；普通开发者签名（免费或付费）拿不到，所以即便装上 App，能力探测也会是 false。

因此本 SDK 的可用前提明确为：**越狱设备（root）或 iOS ≤ 17.0 的 TrollStore 设备**。当前设备不满足，等越狱设备到位再验证。

同时排除了两条不合适的替代路线：

- **WDA（XCUITest）**：能给点击/滑动/输入/控件树，但**没有实时视频流**（只能轮询 `/screenshot`，约 2–5 fps），不满足投屏需求。
- **CoreDevice（USB 隧道）**：iOS 17+ 的 `DisplayService` 确实能推 RTP/H.264-H.265 视频、也能发 HID，且**不需要证书**；但需要 USB 直连 + 开发者模式 + 隧道（Windows 要管理员与 wintun），而且我们的 `coredevice/conn.go` 传输层与视频接收解码都还没写。已确认 pymobiledevice3 11.12.4 实现了该服务，可作为后续自研的协议参照。

**下一步（等设备）**：越狱机到位后，用 CI 产出的未签名 `.ipa` 直接经 TrollStore / root 安装，先跑 `AgovHost` 的三个自证式测试（注入点击看计数、注入上滑看列表、截图看出图），确认 `runtime.capabilities` 全为 true 后再做业务。

iOS 侧的架构认知（与安卓相反）：未越狱场景下被控端不在手机里，而在**连手机的电脑**上。本 SDK 面向的是"手机内运行"的越狱场景，与 host 侧方案是两条独立路线。

## 2026-11-11 深入核查与修正

已核实 AXUIElement 对象缓存、5039 快照与坐标补采调用链，替换此前不等价的 CF 风格属性采集。新增共享刷新、引用释放、错误/取消入口；解释器新增任务独立 Session、类型与构造器绑定、工程与 ZIP、本地导入、embed 预处理。当前仍未证明参考实现和我方全量等价。

Windows 相关 Go 测试与 SDK Go 构建通过；本轮没有 Apple 工具链，新 Objective-C 未在此环境编译，未运行真实设备。历史模拟器统计不能覆盖本轮改动。
