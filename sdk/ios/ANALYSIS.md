# AutoGo iOS 参考依据

本记录区分参考包中的静态证据与我们的实现，不把反汇编结果等同于真机验证。

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
