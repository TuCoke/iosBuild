# 截图与节点树失败定位

2026-11-11。保持 AutoGo 的 CARenderServerRenderDisplay / IOSurface 和 AXRuntime / AXUIElement 路线，不使用 simctl、XCUITest 或 UIKit 自身视图树替代 SDK 成功结果。

静态样本确认：STKit.captureScreenRaw:height:bytesPerRow: 位于 0x1011e938c；0x1011e94bc 的字典项数为 4，分别为宽、高、每像素 4 字节、BGRA 像素格式。0x1011e954c 调用渲染器，参数为 0、LCD、surface、0、0；返回值 0 才继续锁定读取。当前源码额外指定 stride/allocSize，诊断使用独立 surface 做两种属性的对照，不修改现有生产路径。

样本 uiacc._Cfunc_AXBootstrap → 原生 AXBootstrap(0x1010ba3e8) → dispatch_once 初始化块(0x1010bb620)，包含 AX 类加载、几何信息、FullKeyboardAccess 初始化与坐标预热。其临时切换进程身份不直接照搬到并发 Go 宿主。本轮记录真实身份、框架来源、完整异常和任务进程对照后再确定原因。

产物：

- selftest-all.json：完整探针报告，不先截短 AX 异常。
- native-diagnostics-host.json：宿主进程中原有截图/节点树 API 的实际结果。
- native-diagnostics-worker.json：同一动态 SDK、同一 API 在独立任务进程中的结果。
- 记录 pid/euid、UIKit/AX 类所在镜像、截图符号所属镜像、模拟器根路径、系统实际显示名称、四项/六项 surface 属性的渲染返回值。
- AX 成功时返回实际节点和数量、上限及可能截断标记；失败保留异常原文及调用栈，不返回伪造的空树。
- 只有 SDK 正常截图成功时才保存 native-capture-host.jpg / native-capture-worker.jpg；sim.png 仍仅为外部测试截图。

观察方向是对照实验，不预先认定任何候选就是根因：显示名称差异、surface 属性差异、UI 宿主与独立任务进程差异，以及模拟器加载的实际系统镜像差异。
