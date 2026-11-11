# iOS SDK 与 AutoGo iOS 对标状态

更新时间：2026-11-11。主实现为 `v2/sdk/ios/`，`v2/ios/` 是独立 Apple 构建镜像。

**已完成本地纯 Go 编译与静态检查；GitHub 首轮发现检查器漏扫并已修正，原生验证待完成。** [本次检查记录](ios-sdk-ci-20261111.md)。 下文“本轮已补代码”不能理解成已经通过模拟器或真机验收。使用方式、边界与更新流程见 [新增功能说明](ios-sdk-features.md)。

## 样本与已有验证

参考核心为用户提供的 `AutoGo-ios/resources/libs/ios/libgoeval.dylib`，SHA256 为 `b4e157737a9bba0de49fd98ad5d9b5e30519118cacbd63df46e807b0e6f5af48`。它虽名为 dylib，Mach-O 实际是 MH_EXECUTE。闭源样本的符号、调用点和行为用于确认原理，不能从名字相同直接推导所有签名和结果完全一致。

此前版本的证据继续有效，但只覆盖当时提交：

- [9a1900d 的 CI](https://github.com/TuCoke/iosBuild/actions/runs/42987054926) 与 [70e5445 的 CI](https://github.com/TuCoke/iosBuild/actions/runs/42988971066) 曾通过；第二次存在一次 FrontBoard 未注册失败，重跑通过，未放宽断言。
- iPhone 16 Pro / iOS 18.5 / arm64 **模拟器**中，SDK 自身取得正常 JPEG，AX 宿主 79 / 工作进程 80 个节点，注入目标计数 0→1，脚本工作进程正常返回。
- 原生 JPEG 逐张查看过；不是用 simctl 截图替代 SDK 采集。没有实体 iPhone 的完整验收。
- 这些历史结果不覆盖本轮新写的 OpenCV、ImGui 控件、JS UI 桥、资源工具、预热和升级恢复代码。

## 完整能力对照

SDK 路径统一以前缀 `v2/sdk/ios/` 表示；宿主路径另标。

| 能力 | 样本路线 | 我们已有/本轮实现 | 仍缺或未验证 |
| --- | --- | --- | --- |
| 点击、长按、滑动 | IOHID 父 digitizer 与手指子事件 | 既有 `motion/`、`internal/native/touch_ios.m`；模拟器点击曾生效 | 真机、不同 App、长轨迹与边界验收 |
| 多指、双指缩放 | 多个触点及轨迹时序 | 既有完整帧/增量触点、Gesture/Pinch、取消抬手 | 多指交接与复杂手势实测 |
| 坐标与方向 | 尺寸、scale、FrontBoard 方向 | 既有 `display_ios.m`、`types/` | 横竖屏切换、iPad、多机型 |
| 中文、键盘 | Unicode 与 HID 键盘事件 | 既有 `ime/` 和输入桥 | 中文组合、emoji、跨 App 输入验收 |
| 截图 | CARenderServer → IOSurface → CGImage | 既有、模拟器已验证；区分设备返回 0 与模拟器布尔成功约定 | 真机、受保护画面、切换方向 |
| H.264 视频 | VideoToolbox、SPS/PPS、Annex B、PTS | 既有 `video/`、`video_ios.m` | 连续编码、调参、背压、播放、长时间运行 |
| 原始帧 / C→Go 回调 | BGRA 与编码回调 | 既有 streambridge/callbacks | 持续帧、停止重启与回收 |
| AX 树与查询 | AXRuntime/AXUIElement、5039 缓存快照、网格补采 | 既有、模拟器已验证；Go/C ABI 共用主线程采集 | 真机跨 App、弹窗、逐字段等价对照 |
| AX 环境初始化 | 辅助设置、中心预热、身份处理 | **本轮补显式 Warmup**，有界中心查询、设置恢复、保留预热状态 | 默认 Bootstrap 未切换；不修改并发 Go 进程身份，root/mobile 场景仍需验收 |
| 应用管理 | SpringBoard、FrontBoard、LSApplicationWorkspace、helper | 既有 `app/`、installer | 安装、卸载、清数据与启动完整真机验收 |
| 设备与系统 | 系统信息、亮度、剪贴板、锁屏等 | 既有 `device/`、`system/` | 各项授权与机型验收 |
| 基础图像与找色 | Go imaging 与像素运算 | 既有 `images/`；有此前 Go 验证 | 与样本逐像素对照 |
| OpenCV Mat 与匹配 | 原生 OpenCV 与 Go 绑定 | **本轮补代码**：Mat、Scope、编解码、裁剪、缩放、旋转、灰度、阈值、模糊、Canny、FindImage/All；原生 `host/ios/objc/ag_cv.mm` | 未编译/实测；未提供完整 OpenCV 所有类型/函数；模拟器运行库暂缺 |
| PPOCR / YOLO / 点阵 OCR | NCNN/OpenCV 与识别引擎 dylib | 既有引擎适配；本轮补原生资源交付检查 | 实际模型效果、算法自建源码、模型发布清单仍缺 |
| HUD / Console | ImGui + Metal | 既有基础面板；仍在自身 WindowScene | 不是跨 App 全局悬浮 |
| ImGui Go 控件 | cimgui、控件、结构访问器 | **本轮补代码**：44 项生成接口、Checkbox/Slider/InputText、Run 帧回调、触摸与原生键盘代理、任务解绑与异常回收 | 未编译/实测；未覆盖所有结构、绘图对象和完整高级 API |
| 全局悬浮宿主 / 悬浮球 | agoverlayd、floatball、全局窗口注册、触摸 socket | **仍未实现完整独立宿主**；本轮继续核对样本 `SBSAccessibilityWindowHostingController` 和 FrontBoard 场景链 | 全局窗口、CAContext 图层交付、触摸路由、独立宿主看护与退出清理 |
| JavaScriptCore 控制 | 宿主 JS、TCP 8990、消息回调 | 基础链路此前已验证；**本轮补带 ID/超时的双向请求应答** | 新协议分支未运行验证 |
| JS 原生 UI 桥 | 原生对象/结构、控件、动作目标 | **本轮补代码**：AGJSBridge/Selector/Struct/ActionTarget、固定原生控件、属性/层级、事件、SDK 能力调用、按上下文清理与限额 | 未编译/实测；不是任意 ObjC 类/selector 和样本全部私有 API 的完整兼容层 |
| Go 脚本解释 | app/yaegi、SDK/第三方绑定 | 既有上游 Yaegi、工程/ZIP/import/embed；**本轮注册新增能力并绑定 Mat/UI 任务生命周期** | 样本 fork、core 路径及全部第三方签名差异仍未完整核实 |
| 独立脚本进程 | native loader、任务工作进程与看护 | 既有 Runtime/app；已验证正常返回；有硬超时、父进程看护与 waitpid | 故障注入与阻塞原生调用验收 |
| 文件 / Plist / SQLite / HTTP | 文件、属性列表、SQLite、HTTP | 既有相应包和此前验证 | 样本内部 fork 边界不保证完全一样 |
| SDK 更新 | 候选安装、启动加载、健康和恢复 | **本轮补代码**：平台/入口验证、不可变版本目录、pending/attempt/current/previous、健康确认、下次启动回退、手动 rollback | 未编译/实测；依赖 ABI/模型兼容、版本清理、发布信任链和长期健康策略仍需补齐 |
| 原生依赖交付 | 引擎库与模型制品 | **本轮补代码**：7 个库的来源/摘要/平台锁、HTTPS或离线获取、缺件阻止发布、包内清单与运行期检查 | 本轮未获取资源；预编译引擎不是自建源码；模型未纳入统一发布 |
| 穿透 | frpc 与进程生命周期 | 既有客户端定位与源码构建回退 | 外网与长期保活验收 |

`device.wake`、`dotocr.set_dict` 已有分支；环境不支持不等于没有源码。历史 70 项探针中，40 项调用返回成功、4 项环境失败、2 项不支持、6 项跳过副作用、18 项仅参数拒绝，不等于 70 项功能全部通过。

## 本轮文件边界

```text
v2/sdk/ios/
├── opencv/                  Mat、Scope、图像处理与匹配
├── imgui/                   帧回调、控件及 C 包装声明
├── resources/               包内原生依赖检查
├── update/                  平台检查、版本状态和回退请求
├── uiacc/environment.go     显式预热 API
├── internal/native/         OpenCV 分派、AX 预热及宿主平台信息
└── script/                  新能力导出与任务生命周期绑定
v2/ios/
├── host/ios/Sources/         JS 原生 UI 桥、控制应答和 SDK 启动恢复
├── host/ios/objc/            OpenCV 胶水、ImGui 帧/触摸/输入代理
├── host/ios/third_party/     生成控件的 C++ 实现
├── host/ios/native-resources.lock.json
├── tools/                   控件生成器与原生资源获取/校验工具
├── .github/workflows/       发布前缺件校验及新原生源的构建装配
└── sdk/ios/                 同一份能力源码的构建镜像
```

本轮未改 Agent、MCP、云控调度、安卓协议及其已有未提交工作。

## 仍需继续实现的代码

1. **完整独立全局悬浮宿主**。自身窗口不能替代跨 App 能力。样本能确认 `agoverlayd` 的 `OverlayRegisterContextId`、`OverlayClientThread`、`OverlayApplyRegionsOnMain`、FrontBoard 场景初始化，以及 floatball 的 `CAContext remoteContextWithOptions:`；完整 ABI/协议尚不能只凭符号名照写。
2. **OpenCV/ImGui 的其余完整绑定**。本轮是实际可调用的核心实现，不是全部名称覆盖；高级容器、复杂结构所有权和回调签名仍需逐项确认后扩充。
3. **Yaegi fork / core / 第三方兼容层**。现有能力分布在 motion/device/app 等包，不应把缺同路径包装误报为缺所有设备能力；若要求样本脚本原样运行，仍需核对类型、签名、导入路径和行为。
4. **模型与依赖兼容发布**。模型版本、签名清单、宿主/SDK/ImGui ABI 兼容、原生算法可重建源码没有完整闭环。新 ObjC 桥和 libimgui 必须与新宿主一起交付，不能声称只热更 Go SDK 就能升级它们。
5. **更新后续策略**。当前是重启后加载及自动选择已确认版本，不是运行中卸载 Go 库；还缺长期健康观测、旧版本清理与完整发布信任链。

此外，真机输入、方向、跨 App AX、视频/背压、模型识别、全局 UI 与升级恢复仍需后续验收。这些是验证工作，不要靠新增空包或目录冒充完成。

## 已知差异

- 样本元数据最低 iOS 13；我方构建目标 iOS 15，未声称支持 13/14。
- 当前 ImGui 为 1.92.5 / 19250；样本有 WIP / 19244 记录。
- 我方保留独立拥有的帧、context 和结构化错误，不要求复制借用缓冲或并发进程内临时切换身份。
- 新增模板匹配阈值定义、UI 协议和更新状态是本仓库明确约定，不冒称与样本二进制可互换。

[历史函数名清单](ios-autogo-function-inventory.json) 是本轮之前的静态名称对照，只用于定位线索，不能计算完成百分比或替代签名/行为验证。

## 参考记录

- [原生能力与 ABI](ios-autogo-sdk-alignment.md)
- [脚本与宿主对齐](ios-autogo-runtime-alignment.md)
- [截图与 AX 的历史定位和实测](ios-capture-ax-diagnostics.md)
- [本轮新增功能与使用方式](ios-sdk-features.md)
