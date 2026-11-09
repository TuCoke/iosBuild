# iOS 设备能力 SDK

源码位于 `v2/sdk/ios/`，属于现有 `autogov1/v2` 模块。与安卓 SDK 独立；本轮不迁移安卓目录、不改 Agent、不接入 MCP、不添加脚本解释器。

## 代码状态

这是参考 AutoGo iOS 资源包的接口与静态调用关系，独立编写的第一版实现。没有链接、复制或打包 `AutoGo-ios/resources/libs/ios/libgoeval.dylib`。**尚未经过 Apple 工具链编译或 iOS 真机验证，不能称为已经可部署或兼容所有 iOS 版本。**

2026-11-09 补充验证：Windows 下的 Go 编译和 Go 单元测试已通过；iOS/arm64 原生构建因本机缺少 `clang` 而失败。该结果不证明 Objective-C 已可编译，也不证明手机操作已生效。详细命令和范围见 [验证记录](ANALYSIS.md#验证记录)。

| 模块 | 已写入的实现 | 尚需确认的部分 |
| --- | --- | --- |
| motion | 点击、长按、滑动、完整多指帧、双指缩放、输入串行、取消抬手 | IOHID 权限、事件来源、各机型实际响应 |
| ime | UTF-8 Unicode 事件、HID 键盘事件、部分输入失败语义 | 私有 Unicode ABI、组合字符、各应用输入效果 |
| capture | 渲染服务器 → IOSurface → PNG/JPEG、缩放、方向转换 | 系统权限、方向、黑屏及受保护画面 |
| video | VideoToolbox H.264、Annex B、SPS/PPS、PTS、调码率、请求 IDR、有界缓冲、停止释放 | 真机编码表现、CPU/延迟、与现有媒体源约定适配 |
| app | 启动应用、应用列表、查询前台应用 | 私有选择器与权限适配；启动被接受不等于前台已切换 |
| device | 系统版本、型号、电量、显示信息、剪贴板、亮度、系统按键、锁屏请求 | 读取限制、后台行为；不提供密码锁绕过 |
| uiacc | 快照模型、查询、等待、未知状态表达、Provider 接口 | **未实现原生 AX 控件采集，不能直接调用就获取系统控件树** |
| OCR/YOLO | 本轮未接入 | 独立构建 iOS 原生依赖及确认 ABI，不能直接复用安卓二进制 |

`Capabilities` 返回的是运行时入口可用性，不表示授权成功、注入生效或业务达成。未声明原生控件树能力。非 iOS 或关闭 cgo 时，设备调用返回 `types.ErrUnsupported`，不会假装成功。

## 目录

```text
ios/
├── app/                   应用操作
├── capture/               单张截图
├── device/                设备信息和系统操作
├── ime/                   文本、HID 键盘
├── motion/                单指和多指动作
├── types/                 公开数据和错误约定
├── uiacc/                 控件快照查询、采集器契约
├── video/                 H.264 输出、读取和生命周期
├── internal/
│   ├── inputstate/        进程内输入串行闸门
│   └── native/            Go/cgo 与 Objective-C 原生桥接
└── examples/
    ├── basic/             宿主调用基础动作示例
    └── video/             读取视频包示例
```

不创建空的 OCR、识别库或资源目录。真实依赖接入时再添加，避免目录存在却没有功能。

## 使用约定

```go
import (
    "autogov1/v2/sdk/ios/motion"
    "autogov1/v2/sdk/ios/capture"
)

// ctx 由宿主提供；其 UIKit 主线程事件循环必须已经运行。
err := motion.Configure(ctx, 0, 0)
if err != nil { return err }
err = motion.Tap(ctx, 0.5, 0.5)
if err != nil { return err }
frame, err := capture.Screenshot(ctx, capture.Options{JPEG: true, MaxSize: 960})
```

- 触摸坐标是当前显示方向的 `0..1`；手指 ID 为 `0..9`。`TouchFrame` 每次提交所有按下手指，空列表抬起全部手指。
- 方向采用自然显示方向顺时针旋转 `0/1/2/3` 个四分之一圈。`motion.Configure`、截图和视频必须使用同一方向。SDK 不从宿主 App 的方向猜测另一个前台应用的方向；宿主检测旋转后显式更新。改变视频方向需关闭旧流并按新配置启动。
- `device.Display` 是自然方向原生像素及 nativeScale，不将逻辑点与截图像素混为一谈。iPad、横屏及缩放显示仍需真机核对。
- `device.Info.Identifier` 是可获得的 vendor 标识，可能为空，不保证跨重装稳定。云控的设备 ID 应由 Agent 自己持久化管理。
- `ime.Key` 接收 USB HID Usage Page `0x07` 的键值，不能传入安卓 keycode。iOS 没有通用的安卓返回键。
- 输入动作进程内串行；这不替代云端租户权限、设备 Session、跨进程互斥或业务验证。
- 仅使用单张截图后，可调用 `capture.Release(ctx)` 释放缓存的采集 surface；已返回的图片数据仍独立有效。
- 已提交的输入可能没有业务效果；`ErrUnknown` 不能自动重试。没有实现“一个通道不确定后换另一个通道重放”。
- 所有公开调用接收非空 context。排队和 Go 动作等待可取消；进行中的同步原生调用无法强行中断。UIKit 请求最多等待约三秒，尚未开始的请求会取消，已经开始但没返回的请求报告结果未知。

## 视频生命周期

```go
stream, err := video.Start(ctx, video.Config{
    MaxSize: 960, Bitrate: 1500000, FPS: 15,
})
if err != nil { return err }
defer stream.Close()

for {
    packet, err := stream.Read(ctx)
    if err != nil { return err }
    // packet.Data 为 Annex B；packet.PTS 是相对启动时间。
    // 交给宿主传输层；SDK 不在这里连接 WebRTC、WebSocket 或云端。
}
```

- 每个进程只允许一个视频流；token 防止旧流的关闭请求影响新流。
- `Start` 的 context 是流的生命周期；取消时关闭原生流。仍应显式 `Close` 并处理错误，不能用永不取消的 context 打开后遗忘句柄。
- `Read` 没有无限消费者协程；原生读取每次最多等待约 100ms，再检查取消。
- 编码在途帧限制为两帧，避免采集速度大于编码速度时无限堆积。
- 缓冲按字节和包数限制。积压时清空队列、请求 IDR，并丢弃依赖已丢帧的 P 帧；IDR 前附 SPS/PPS。单个包超过上限时流失败，不能无限增加内存。
- 采集、编码失败会停止定时器与编码器。读取失败或调用 `Close` 后释放会话；关闭时清理缓冲和采集 surface。
- SDK 没有用户回调运行在编码线程上。消费者应给自己的网络写入设置超时。
- `SetBitrate` 和 `RequestKeyFrame` 是控制请求，实际码率及关键帧效果需要从输出和真机确认。

## 构建与宿主环境

原生文件只参与 `ios && cgo` 构建。需要 Apple 工具链、真实设备 SDK 和 arm64 目标；Windows 上的 stub 可导入，但不能操作手机。Objective-C 文件当前使用手工引用计数，不应全局强制 ARC。

本 SDK 使用 UIKit，宿主必须已经运行主线程事件循环，且不能阻塞主线程等待执行 SDK 的 Go 工作线程。`examples` 是宿主调用示例库，不是可直接安装的 IPA。单独的 Go 命令行 bin、iOS 启动器、签名、安装、权限配置、Agent 入口和热更新均未在本轮实现。

私有符号可用性与系统授权是两回事。当前实现不安装提权工具、不修改系统授权、不执行漏洞。TrollStore、越狱或其他已授权运行环境的适用系统版本需要单独确认，不能根据包中出现某个函数推断所有 iPhone 都可运行。

## 后续 Agent 接入

保持 `tap/swipe/text/screenshot/open_app` 等命令名；由独立的 iOS 命令注册代码调用本 SDK。Agent 公共核心仅在设备信息、心跳、缩略图和视频源处解除安卓依赖。SDK 自身不反向导入 Agent。

视频接入前必须核对现有媒体源的时间戳、Annex B、参数集、关键帧和停止语义；本次没有宣称已经接入现有 WebRTC。

详见 [静态分析依据与未确认项](ANALYSIS.md)。
