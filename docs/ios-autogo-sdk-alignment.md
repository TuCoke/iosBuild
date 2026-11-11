# iOS SDK：按 AutoGo 技术栈与调用逻辑对齐

更新：2026-11-11。本轮覆盖用户图片中的全部分组，不改云控 Agent、MCP 或服务端路由。

本文描述新源码状态，取代上一轮核查中相关“仍未修复”的条目。对齐依据是提供样本的函数调用、寄存器/栈参数、ObjC 方法和内存释放路径；不是从函数名称猜测 C 原型。新源码已于 2026-11-11 通过 GitHub Apple 工具链编译、完整打包和模拟器启动流程；真实 iOS 设备仍未验收。不能继承参考程序的测试结论，也不宣称与闭源程序逐字节等价。

## 能力与实现位置

主能力源码在 `v2/sdk/ios`，独立构建镜像在 `v2/ios/sdk/ios`；本轮对应文件同步。ObjC++ 识别胶水仍在已有的 `v2/ios/host/ios/objc/ag_rec.mm`，由宿主构建为原生静态库。

| 图片中的能力 | 本轮实现方式 | 主要文件 |
| --- | --- | --- |
| 点击、长按、滑动 | IOHID digitizer 父事件及手指子事件；像素简版直接走原生逐指状态；完整动作持有输入闸门 | motion/touch.go、motion/motion.go、internal/native/touch_ios.m |
| 多指、双指缩放 | 增量触点和完整帧共用一份原生 contacts；抬起一个编号保留其余手指；Gesture/Pinch 提供多指时序与取消清理 | 同上、internal/native/ag_bridge.m |
| 坐标转换 | FBSOrientationObserver 观察前台方向，原生尺寸与 scale；像素坐标按当前方向转换，高级手势开始时冻结方向 | internal/native/display_ios.m、types/types.go、device/status.go |
| 中文、键盘输入 | Foundation 组合字符边界 → UTF-8 → IOHID Unicode；每四段间隔 15 ms；HID 按键按样本保持 100 ms 后抬起 | ime/ime.go、internal/native/touch_ios.m |
| 截图 | CARenderServerRenderDisplay → IOSurface → UICreateCGImageFromIOSurface；图像复制或缓冲回退，统一旋转 | internal/native/capture_ios.m、capture/capture.go |
| H.264 | VideoToolbox、Annex B/SPS/PPS、PTS、有界输出；C 回调与 Go 拉取共用编码器 | internal/native/video_ios.m、video/video.go |
| C → Go 回调桥 | 编码及 BGRA 原始帧真实送入 Go；注册、启动、停止、错误和内存所有权均有代码；慢消费者丢弃后请求关键帧 | internal/native/streambridge_ios.m、callbacks_ios.go、video/callbacks.go |
| 控件树采集 | AX 对象缓存、5039 快照、递归展开、坐标补采、字段归一化与去重 | internal/native/ax_runtime_ios.m |
| 控件查询 | 快照筛选、文本/属性条件、带 context 的查找与等待；点击保留实际动作错误 | uiacc/selector.go |
| 控件采集生命周期 | 引用计数、采集后等待 100 ms、失败不继续使用旧树、最后释放停止、脚本任务结束自动释放 | uiacc/cache.go、script/script.go |
| AX 环境初始化 | libAccessibility 私有 setter/getter、已开启时刷新、CFPreferences 回退、环境报告、显式恢复及恢复后重新引导 | internal/native/ax_runtime_ios.m、uiacc/environment.go |
| 应用管理 | LSApplicationWorkspace/Proxy、FBSSystemService、已安装 TrollStore helper、dpkg/uicache；修正调用签名、列表解析与数据容器处理 | internal/native/app_ios.m、installer_ios.m、app/manage.go |
| 设备与系统 | 修正锁屏 ABI、Darwin lockstate、SBSUnlockDevice、MobileWiFi AllowEnable；保留已有系统状态与亮度能力 | internal/native/device_ios.m |
| 图像算法 | 按样本 Go 调用链：Lanczos 缩放、任意角度旋转、GrayModel 灰度、阈值/自适应阈值；找色仍复用公共算法，连续帧使用原生回调 | images/images.go、sdk/imaging |
| OCR、YOLO、点阵识别 | 使用相同 NCNN/OpenCV/识别运行库；按实际 RGBA C ABI 接入，实例化、加载、复用、关闭、返回字符串释放与区域偏移 | ppocr、yolo、dotocr、host/ios/objc/ag_rec.mm |

## 已核实的关键参数

主样本：`AutoGo-ios/resources/libs/ios/libgoeval.dylib`。

SHA256：`b4e157737a9bba0de49fd98ad5d9b5e30519118cacbd63df46e807b0e6f5af48`。

- 触点：STKit 的 touchDown/Move/Up 分别位于 `0x1011e9afc`、`0x1011e9df8`、`0x1011e9e6c`。手指编号范围 0–9；子事件 identity 为 2，状态变化掩码包含 position/range/touch；父子事件设置 display-integrated 字段。
- 方向：FrontBoard 的系统方向 2/3/4 分别映射到四分之一圈 2/3/1，其他映射为 0。不是宿主页面的 UI 方向。
- 简版 Click：样本等待约 10–20 ms。Swipe 按约 10 ms 分段、使用绝对时间补偿；Swipe2 使用二次贝塞尔，法线偏移取 `0.4 * min(distance, 200) * (random/1000 - 0.5)`。
- 按键：BridgePressKey 是 page + usage 两个参数，样本按下后等待 100000 微秒再抬起；任务切换是两次 Home，中间约 20 ms。
- NativeSize：两个 double 输出指针，不是字符串缓冲区。GetDisplayInfo 的简版旋转返回角度；底层 Display.Rotation 使用 0–3。
- Unicode：`rangeOfComposedCharacterSequenceAtIndex:` 分段，不能简单拆 rune；回调为 UTF-8 指针和字节长度，六参数 ABI 已核实。
- 截图：样本 `captureScreenRaw` 检查 CARenderServerRenderDisplay 的整数返回值，0 才继续；此前 void 声明及“没有返回值”的注释已纠正。
- 视频：H.264 参数为 resolution/bitrate/fps 三参数；RawFrameStart 无参。H.264 回调为 data + int length，原始帧回调为 data/width/height/stride。
- AX traits：button/clickable=1、selected=8、disabled=0x100、adjustable=0x1000、header=0x10000、textEntry/editable=0x40000、statusBar=0x800000、password=0x1000000、toggle=0x2000000。Switch-like 还检查 bit 53 和 role/class 字符串。掩码及输出槽位来自 `0x1010bde7c` 起的实际数据流。
- 强杀：FBSSystemService 的 terminateApplication 首个参数是 bundleID 对象，样本 reason=5；不是 pid。
- 安装：样本使用 helper 的 `install <path>`、dpkg 的 `-i`/`-r` 和 uicache；子进程 persona 设置与当前进程临时切身份是两种不同机制。我们只在已有授权环境通过 persona 启动独立安装进程，失败返回错误。

## 识别 ABI 修正

原来的适配器把“函数名存在”误当成“参数已确认”，将识别入口声明为 cv::Mat/输出缓冲，存在内存错误风险。已按 cgo 的实际寄存器与栈布局修正：

```c
void *ppocr_new(int version);                     // 样本 v2=2，其余默认=5
int ppocr_load(void *engine, int flag);            // 样本传 1，0 表示加载成功
char *ppocr_ocr(void *engine, const uint8_t *rgba,
               int width, int height, const char *color);
void ppocr_close(void *engine);

void *newYolo(void);
char *loadModelYolo(void *engine, const char *version,
                    const char *param, const char *bin,
                    const char *labels, int threads); // 返回字符串 OK 才成功
char *detectYolo(void *engine, const uint8_t *rgba,
                 int width, int height,
                 float probability, float nms, int targetSize);
void closeYolo(void *engine);

void dotocr_setDict(const char *name, const char *dictionary);
char *dotocr_detect(const uint8_t *rgba, int width, int height,
                    const char *color, float similarity, int asJSON,
                    const char *dictionary, int offsetX, int offsetY);
void dotocr_findStr(const uint8_t *rgba, int width, int height,
                    const char *color, float similarity,
                    const char *text, const char *dictionary,
                    int offsetX, int offsetY, int *outX, int *outY);
```

返回的 char* 由调用方 free。labels 是标签内容，不按模型文件路径读取。YOLO 默认 probability=0.35、nms=0.25、targetSize=640。

现在的模型实例有独立 token 和锁，加载失败不返回假成功；重复检测复用已加载模型，Close 关闭真实句柄。Go 简版新增 Error 方法。解释器中的模型构造器绑定任务 context，任务结束触发 Close。

相应参考库版本：

| 文件 | SHA256 |
| --- | --- |
| libppocr.dylib | 3d24591d675b8e1d2c10b6850aaf869c8887525fb2b37ac6a6597d6420158de7 |
| libyolo.dylib | 9f88bbe508f87f7c1058344c0607b9c77a215c6436d68b5185f1d71d50b39429 |
| libdotocr.dylib | 802372f5e4fd1cfa72c6fc3c586efb0ac219d8fe00bf28f90f0cc5d75df74b10 |

以上为原始参考文件摘要；codesign/install_name_tool 会改变文件摘要，不能直接将它们硬编码成安装包签名后的校验值。未来替换识别运行库必须重新核对 ABI。

## 保留的工程边界与明确差异

- 保留我方 context、可分类错误、输入串行与取消清理，不改对外云控协议。
- 原始帧进入 Go 后复制为独立内存，可跨回调保留；这是有意区别于借用缓冲的内存约定。
- 完整多指帧一次构造父事件及多个子事件，而样本逐指入口发送增量事件；二者共用同类 IOHID 技术，但不宣称事件字节序列相同。
- 我方保留原生队列拉取视频作为另一种 API；回调和拉取共享编码器，同一时间只能选择一种。
- AX 有效身份为 mobile 的宿主直接初始化。没有在运行中的多 goroutine Go 进程里临时切换整个进程 euid/egid；样本中的这项做法没有直接照搬。root 任务宿主的 AX 跨应用表现仍需单独验收，不能算完全等价。
- 图像处理已进一步按样本调用链改为 Lanczos/任意角度旋转/GrayModel/阈值语义。找色算法仍复用公共实现，尚未逐函数证明与样本逐像素等价；完整 OpenCV Go Mat/FindImage 绑定仍未提供，不能把识别依赖当成完整绑定。
- 不加载闭源 libgoeval 充当我方 SDK。原样依赖的识别运行库也不等于这些算法已经成为我方可重建源码。

## 图像处理调用链勘误

此前“AutoGo images.Resize 走 OpenCV”的代码注释不正确，已删除。实际静态证据：

- `images.Resize` (`0x1007d8870`) 调用 `images/imaging.Resize`，传入 support=3 的滤波器；kernel (`0x1007d1e20`) 为 `sinc(x) * sinc(x/3)`，即 Lanczos。
- `images.Rotate` (`0x1007d8920`) 将角度取负后调用 `images/imaging.Rotate`，背景对象的方法是 `image/color.(*Alpha16).RGBA`，值为 0，即透明背景。
- `images.Grayscale.func1` (`0x1007d8b80`) 通过 `color.GrayModel.Convert` 转换 NRGBA，透明度参与灰度计算，不能直接按不透明 RGB 加权。
- `images.ApplyThreshold` (`0x1007d8d10`) 对未知类型保留灰度；BINARY 在相等阈值时输出零。
- `images.ApplyAdaptiveThreshold.func1` (`0x1007d9260`) 使用浮点 C；MEAN_C 边缘跳过越界像素但仍除 blockSize²，GAUSSIAN_C 的 sigma 为 blockSize，并按有效邻域权重归一化。

我方缩放/旋转复用公开的 [disintegration/imaging](https://github.com/disintegration/imaging) v1.6.2（MIT，许可文本位于 `v2/third_party/licenses/imaging-MIT.txt`）；这是按核实算法选择的公开实现，不宣称已确认样本内部 fork 的具体提交。间接依赖 x/image 固定 v0.38.0，与当前 Go 1.25.1 保持兼容。灰度和阈值按上述语义重写，并行处理采用有界 worker，不按屏幕行数无限创建 goroutine。

新增图像验证覆盖：缩放插值与等比尺寸、45°/90° 旋转、透明像素、非零图像原点、阈值相等/未知类型、边缘均值与浮点 C。它们验证我方计算，不代表已经把闭源样本作为黑盒运行并逐像素比对。

## 宿主与独立执行体

- 界面按钮与启动探针改为后台串行调用 SDK；UIKit 更新仍回主线程，避免解释器等待主线程时外壳又阻塞主线程。
- SDKLoader 加载过程同步，防止重复 dlopen/初始化。
- 动态库和模型资源按真正的外层 .app 根定位，支持 `.app/Runtime/app` 独立执行体。
- Runtime/app 固定真正主线程并运行有存活 source 的 CFRunLoop；HTTP/脚本在其他 goroutine 执行，避免 AGOnMain 永远等不到主线程。
- HTTP 请求 context 传入解释器。原生调用仍无法强制抢占，进程看护与云端完整任务接入属于后续装配，未在本轮宣称完成。

## 验证

Windows/Go 1.25.1、GOWORK=off：运行相关 Go 测试、SDK Go 构建和静态 precheck；平台相关 Objective-C、ObjC++、cgo 回调和系统授权未在此机器编译或执行。

本轮增加关闭模型后禁止重新加载、回调参数边界、触点参数拦截等 Go 验证。既有脚本类型调用、任务隔离、取消、ZIP/本地包/embed、选择器生命周期验证继续执行。

补充修正：AX 显式恢复后可以再次 Prepare，首次环境基线不会被覆盖；截图符号初始化支持并发；逐指抬起使用调用方终点；安装子进程非零退出或超时返回结果未知，防止把部分执行误当作未执行而自动重试。

这些测试不证明真实 HID、AX、编码、识别或安装已在手机生效；precheck 的括号/符号检查也不等于 Apple 编译成功。本轮后续已在 GitHub macOS runner 完成编译（结果见下）；下一步仍需在受支持的已授权 iOS 设备验收各能力。

## 2026-11-11 GitHub 原生编译结果

- 构建仓库：`TuCoke/iosBuild`，验证分支 `codex/ios-sdk-alignment-20261111`。
- 代码提交：`70e4ef52bd961225632591bcadc3b88ba7a3d8ca`。
- [Actions 完整记录](https://github.com/TuCoke/iosBuild/actions/runs/42966127885)：四个 job 均为 success，包含 Go 单测、真实 iPhoneOS SDK/CGo 编译、完整 App/任务执行体链接打包、模拟器构建与启动截图。
- [IPA/DEB 安装包](https://github.com/TuCoke/iosBuild/actions/runs/42966127885/artifacts/13831721968) 和 [模拟器截图/应用日志](https://github.com/TuCoke/iosBuild/actions/runs/42966127885/artifacts/13831935812) 已上传为 Actions artifacts，受仓库保留期限制。
- 首轮 ObjC++ 编译发现 `id` 的三元表达式不能直接访问 `UTF8String` 属性；已在 `ag_rec.mm` 改为显式 NSString，并检查 color 参数类型，随后重新完整构建通过。

模拟器应用日志确认：动态 SDK 已加载，内置解释器脚本执行返回，目标块点击计数 `0 → 1`。这是一台 GitHub iPhone 16 Pro / iOS 18.5 模拟器的结果，不是实体 iPhone 的验收。

**尚未通过的运行项：** SDK 内部截图返回 `屏幕渲染失败: 1`，`capture.json` 和截图按钮均记录失败；因此不能因 Actions 为 success 就认定 SDK 截图或视频已生效。CI 的 `sim.png` 是外部 `simctl` 截图，与 SDK 的 CARenderServer 截图是两个入口。真实设备截图、视频、AX、识别及安装能力仍需分别验收。
