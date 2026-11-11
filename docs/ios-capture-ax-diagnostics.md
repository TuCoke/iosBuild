# 截图与节点树失败定位

2026-11-11。保持 AutoGo 的 CARenderServerRenderDisplay / IOSurface 和 AXRuntime / AXUIElement 路线，不使用 simctl、XCUITest 或 UIKit 自身视图树替代 SDK 成功结果。

**最新更正：前几轮把模拟器返回的 1 当成错误，这一判断有误。** 真机样本以 0 继续读取，但 iOS 18.5 模拟器外层将内部有效回执转换成非零布尔值。以下各轮“截图失败/错误 1”是当时 SDK 判断的历史记录，不代表系统渲染实际失败。已按构建目标修正，并通过 SDK JPEG 实际内容验收，结果见末节。

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

## 第二轮修复验证（95cdb4e）

[GitHub Actions 42981319101](https://github.com/TuCoke/iosBuild/actions/runs/42981319101) 四项任务通过。共享采集器通过现有 AGOnMain 边界执行，跨线程传拥有内存的 JSON，不捕获调用方错误指针。C ABI 和 Go API 都复用这一实现。

- 界面进程实际取得 81 个节点；独立任务 PID 8197 取得 82 个节点、1671ms 完成。两边都识别到 `能力探测` 按钮及有效坐标，CI 已将此作为断言。
- 节点数量会随日志文字、界面状态改变，不使用固定数量作为通过条件。
- 这验证了本次 AX 异常的线程修复，不等同于所有 iOS 版本与真机授权环境都已验证。
- 截图外层函数在 0x18b4ffc7c：调用 `CARenderServerRenderDisplay_` 后执行 `cmp x0,#0; cset w0,ne`。因此外部的 1 只是内部非零返回的归一化结果，不是详细错误码；也不是一个无条件返回失败的模拟器占位实现。
- 下一轮只在诊断探针中按该调用点的 ABI 获取内部原始返回，正常截图仍走样本相同的外层符号入口。这些都是私有 API，不能把“可解析的符号”理解为 Apple 公开稳定协议。未证明原因前不把截图失败笼统归为“模拟器不支持”。

## 内部返回验证（b2cfd60）

[GitHub Actions 42984098968](https://github.com/TuCoke/iosBuild/actions/runs/42984098968) 四项任务通过，AX 再次得到宿主 81 / 任务进程 82 个节点，并识别到真实按钮。截图仍失败。

`CARenderServerRenderDisplay_` 在该模拟器有符号信息但未导出，直接 dlsym 返回不可用。诊断只在非 arm64e 的 ARM64 构建中，从外层函数前 64 字节的真实 BL 调用点定位；必须同时满足相同 QuartzCore 镜像、完整符号名称匹配，才调用已核实 ABI 的内部入口。不使用固定地址，不修改指令，不在正常截图路径启用此诊断。

八组调用全部返回 `0x8000000000000000`，外层将其转换成 1。没有将此值强行解释为 Mach 错误码、C 字符串或 CF 对象，也没有因为它不是可读字符串就宣称缺少权限。继续核对设置该标记的具体分支及分配后缓冲区实际属性。

中间 b1d10ef 的首次模拟器运行停留在系统 App 启动阶段较久，取消后取回的完整应用诊断也得到 AX 81 / 82。该次为 cancelled，不记为完整 CI 成功；b2cfd60 的独立完整通过记录用于当前验收。

## 运行身份差异（尚未判为截图根因）

AutoGoRunner 的 `AGKit spawnAsRoot:arguments:` 位于 0x10001cb24，调用 0x10001cd38 的 helper。helper 在已为 root 时尝试直接 spawn；否则使用 persona 99、override 标记 1、uid/gid 0 的子进程属性，并有失败后普通 spawn 的分支。因此“提供 root 启动路径”不等同于“任意环境必定以 root 运行”。

本项目安装器已有 persona 启动接口，但本轮 Runtime/app 经普通子进程入口运行，实际 uid/euid 均为 501。这是需要独立核对的宿主启动/授权环境差异。当前没有相同真机环境的身份对照结果，不能将其直接判为截图失败原因，也不能仅凭包内 entitlements 就认定权限已经生效。

## 返回语义定位（6987423）

[GitHub Actions 42985245166](https://github.com/TuCoke/iosBuild/actions/runs/42985245166) 四项通过，AX 仍为 81 / 82 个节点。实测 IOSurface 宽 1206、高 2622、stride 4864、分配 12753408 字节、BGRA 格式，CFGetTypeID 与 IOSurfaceGetTypeID 一致。

读取同一进程的系统函数分支后确认：内部 0x18b4fe1d0 比较传输/应答状态，0x18b4fe1d4 在状态为零时返回应答中的 x19，否则返回零；外层 0x18b4ffca8 的 `cmp x0,#0; cset w0,ne` 将有效非零应答转换成 1。实测原始值 `0x8000000000000000` 正是该非零应答，不能称作“错误标记”。

AutoGo 真机样本的函数指针确实直接由 dlsym(CARenderServerRenderDisplay) 得到（0x1011e8618 → 0x1011e8620），其 0x1011e9550 的 cbz w0 进入读取分支。这说明不能将真机样本的返回约定直接套到模拟器实现。

修正只增加 TARGET_OS_SIMULATOR 下的返回解释：模拟器非零视为成功，真机保持参考样本的零成功约定。采集、surface、图像转换和 SDK 对外接口不更换。CI 新增宿主/独立进程 JPEG 尺寸、文件头与脚本截图成功断言；仍需查看实际图像，不能只凭改了布尔判断宣称成功。

## 最终验收（9a1900d）

[GitHub Actions 42987054926](https://github.com/TuCoke/iosBuild/actions/runs/42987054926) 的 Go 检查、iPhoneOS/CGo 编译、App 打包、模拟器功能验证四项全部通过。

| 入口 | SDK 实际截图 | AX 实际节点 |
| --- | --- | --- |
| 界面宿主 | JPEG 235×512，36782 字节 | 79 |
| 独立任务进程（PID 8296） | JPEG 235×512，38120 字节 | 80 |

已逐张查看产物 `native-capture-host.jpg` 与 `native-capture-worker.jpg`：两图均可见 AgovHost 标题、四行测试文字、红色点击计数区和按钮，方向正常、不是空白图。图片由 SDK 的 CARenderServer / IOSurface / 图像编码生成，`sim.png` 不计入这一结果。

脚本进程 PID 7949，258ms 完成；实际调用 capture.json 返回 23318 字节、235×512，result.captureSucceeded=true、result.ok=true。effectVerified 仍为 false，因为该字段未验证具体业务效果，未将其改成成功。

AX 继续通过“真实按钮文字 + 按钮属性 + 有效坐标”断言；节点数量会随界面日志变化，79/80 与先前 81/82 不构成回归。全量探针为调用成功 40、环境失败 4、不支持 2、跳过有副作用 6、仅参数拒绝 18、未运行 0，不能解释成 70 项能力全部已验收。

结论：本次两个故障分别为 AX 的调用线程边界和模拟器的渲染返回值解释，均已修复。uid 501 环境下截图实际通过，因此不把 root 启动差异归为本次模拟器截图的原因。真机保持样本返回约定，仍需真机分别验收输入、截图、视频、AX 与识别能力；本轮没有继承 AutoGo 的真机测试结论。

## 收尾复核（70e5445）

内部渲染 ABI 探针已限定为模拟器，不在未核实的真机环境执行该内部调用。[最新构建 42988971066](https://github.com/TuCoke/iosBuild/actions/runs/42988971066) 四项全部通过，包含同一套原生 JPEG、脚本截图和 AX 真实按钮断言。

该构建第一次模拟器任务在启动时收到 FrontBoard `Application is unknown`，未执行到 SDK，故未产生诊断；保留失败日志后单独重跑模拟器任务通过。没有为此放宽任何 SDK 断言，也没有把未启动应用的情况记为 SDK 通过。
