# 截图与节点树失败定位

2026-11-11。保持 AutoGo 的 CARenderServerRenderDisplay / IOSurface 和 AXRuntime / AXUIElement 路线，不使用 simctl、XCUITest 或 UIKit 自身视图树替代 SDK 成功结果。

静态样本确认：STKit.captureScreenRaw:height:bytesPerRow: 位于 0x1011e938c；0x1011e94bc 的字典项数为 4，分别为宽、高、每像素 4 字节、BGRA 像素格式。0x1011e954c 调用渲染器，参数为 0、LCD、surface、0、0；返回值 0 才继续锁定读取。当前源码额外指定 stride/allocSize，诊断使用独立 surface 做两种属性的对照，不修改现有生产路径。

样本 uiacc._Cfunc_AXBootstrap → 原生 AXBootstrap(0x1010ba3e8) → dispatch_once 初始化块(0x1010bb620)，包含 AX 类加载、几何信息、FullKeyboardAccess 初始化与坐标预热。其临时切换进程身份不直接照搬到并发 Go 宿主。本轮记录真实身份、框架来源、完整异常和任务进程对照后再确定原因。

进一步核对 0x1010bbb08：只有原 euid 为 0 时才临时切换到 501；若完整键盘控制原先开启，先关闭并等待 100ms，随后开启，在屏幕中心调用 uiApplicationAtCoordinate:，最多尝试 50 次、每次间隔 50ms；0x1010bbd58 随后关闭该设置，并恢复之前切换的身份。当前源码缺少这段中心预热，且保留开启状态。此差异已确认，但是否导致本次错误仍要由完整异常和进程对照判断，不能仅凭静态差异认定根因。

产物：

- selftest-all.json：完整探针报告，不先截短 AX 异常。
- native-diagnostics-host.json：宿主进程中原有截图/节点树 API 的实际结果。
- native-diagnostics-worker.json：同一动态 SDK、同一 API 在独立任务进程中的结果。
- 记录 pid/euid、UIKit/AX 类所在镜像、截图符号所属镜像、模拟器根路径、系统实际显示名称、四项/六项 surface 属性的渲染返回值。
- AX 成功时返回实际节点和数量、上限及可能截断标记；失败保留异常原文及调用栈，不返回伪造的空树。
- 只有 SDK 正常截图成功时才保存 native-capture-host.jpg / native-capture-worker.jpg；sim.png 仍仅为外部测试截图。

观察方向是对照实验，不预先认定任何候选就是根因：显示名称差异、surface 属性差异、UI 宿主与独立任务进程差异，以及模拟器加载的实际系统镜像差异。

## 第一轮实际结果（39878ec）

GitHub Actions 42980006052 四个任务通过；iOS 18.5 / arm64 / iPhone 16 Pro 模拟器。

- 宿主 PID 5933：AX 异常完整原文为 `We're calling into AX runtime off the main thread!`，栈落在 `_handleNonMainThreadCallback` → `_AXXMIGCopyMultipleAttributeValues` → `uiApplicationAtCoordinate:`。不能把它解释为系统没有 AX 能力。
- 独立脚本 PID 6169：866ms 完成，使用同一 SDK 和 AXUIElement 采集到 83 个真实节点，包含测试界面的按钮、标签及其框、属性。没有使用 UIKit 视图遍历替代 AX。
- 截图在两个进程内都失败；LCD/TVOut × 样本四项/显式六项属性，共八次渲染均返回 1，实际 stride 均为 4864。因此这次不能归因于额外 stride 参数或 LCD 名称。
- UIKit、AXRuntime、QuartzCore、IOSurface 均来自 iOS 18.5.simruntime 的 RuntimeRoot，未发现误载 macOS 框架的证据。
- 渲染器前 32 字节是正常 ARM64 函数序言，还不能据此声称它是直接返回 1 的模拟器占位函数。继续读取有界函数入口及直接调用点，区分客户端参数与下游渲染服务失败。

下一轮将共享 AX 采集器统一放在已有主线程边界执行，验证界面进程自查询的同步回调线程约束；独立脚本进程仍调用相同的 AX API。截图暂不替换路线。
