# AutoGo iOS 运行逻辑对齐记录

日期：2026-11-11。主能力目录为 `v2/sdk/ios`；独立 Apple 构建镜像为 `v2/ios`。本轮不修改 Android、Agent 或 MCP。

## 参考依据和范围

以用户提供的 `AutoGo-ios/resources/libs/ios/libgoeval.dylib` 为核心依据，SHA256 为 `b4e157737a9bba0de49fd98ad5d9b5e30519118cacbd63df46e807b0e6f5af48`。该文件虽然名为 dylib，实际是 `MH_EXECUTE`；编译元数据为 Go 1.25.5、模块 app、c-archive。通过静态反汇编确认调用关系，不能据此继承闭源程序的真机测试结论。

`unpacked/ipa/Payload/AutoGoRunner.app/Runtime/app` 是约 5.2 MB 的示例载荷，包含 Hello 示例，不能代替上述真实 SDK 的证据。目录中的 `resources/ios-sdk-mac.zip` 包含本项目早期源码，也不能用作 AutoGo 原始源码证据。

本轮按证据独立实现对应行为，没有把闭源 libgoeval 改名打包。公开 Android AutoGo 仓库仅用于辅助核对；不能将 Android 的 Rhino 结论套到 iOS。

## 已按样本纠正的行为

| 能力 | 已核实的样本行为 | 本轮改动 |
| --- | --- | --- |
| 文件根目录 | 执行文件所在目录 | files 默认根不再指向 HOME；工作进程启动时切到执行目录 |
| 文件 API | GetExtension 去掉点；Rename/Move 按完整目标路径使用 os.Rename | 修正扩展名语义和相应图片保存分支；修复空目录 EOF 判断 |
| 键值存储 | database/sql + sqlite3，执行目录 storages.db，默认 storages 表，key/value 字段 | iOS 使用 mattn/go-sqlite3 v1.14.27；非 iOS 检查使用真实 SQLite；分表 SQL 参数化、Clear 使用 DELETE 保留表 |
| Plist | 支持苹果属性列表数据 | 补齐二进制/XML、嵌套字典、数组、日期和 data，写失败如实返回 |
| 宿主控制 | TCP 127.0.0.1:8990，messageID + length + status，大端；长度前缀字符串 | 删除轮询设计；建立请求匹配、事件推送、等待事件应答、断连唤醒和任务回调释放 |
| JS 引擎 | 原生壳链接 JavaScriptCore；存在 AGAppCtlSession | 宿主使用 JavaScriptCore，每连接独立上下文；目前实现 Eval/消息/生命周期基础通道，完整 AGJSSelector/Struct/ActionTarget UI 桥仍未对齐 |
| 脚本入口 | GoMain → chdirToExecutableDir → main → runGoFile/runZip | Runtime/app 改为原生薄启动器，加载与宿主相同的动态 SDK 执行 Go/ZIP 后退出；Go CLI 保留为开发示例 |
| 进程隔离 | 壳包含 posix_spawn 等进程管理能力 | 宿主 script.run/remote 使用独立工作进程，启动前 chmod(0755)、等待结束、超时终止、waitpid 回收；终止未知时隔离直到确认退出；参数/结果文件为本项目明确自定义协议 |
| 更新目录 | 更新与实际加载路径必须对应 | Documents/Plugins 由原生解析；安装前校验 Mach-O/arm64/dylib 和可选 SHA256；临时文件同步后原子替换；重启生效 |
| 加载来源 | 不能把候选文件当实际已加载库 | sdk.status 使用 dladdr 返回真实加载路径，并分别显示候选文件与加载文件摘要 |
| 悬浮层 | ImGui + Metal，有独立全局宿主辅助进程 | 统一现有 C/Go 渲染入口、切主线程、分配不同面板 ID，修复绘制开关/帧参数/GPU 回调竞争；全局窗口辅助进程仍未实现 |
| 远端脚本扩展 | 此部分属于本项目扩展，不宣称完全等同样本 | 下载继承 context、限制大小、校验摘要格式、防缓存路径穿越；仅执行成功后原子替换缓存，执行失败不重放旧动作 |
| 自检 | 符号存在不等于操作生效 | 将触摸提交、截图成功、效果确认分开；示例不再硬编码设备成功；按错误类别判断不支持 |

### 宿主控制协议的静态证据

- apkctl.ensureConn `0x1007c56a0`：net.Dial("tcp", "127.0.0.1:8990")。
- apkctl.writeFrame `0x1007c6a80`：大端 messageID、帧体长度；帧体开始为大端状态/操作码。
- apkctl.Eval `0x1007c5230`：请求操作码 1；有效负载是 contextID、JS source 两个长度前缀字符串。
- apkctl.startReader.func1 `0x1007c59a0`：状态 5 为事件，6 为需应答事件；其他按 messageID 匹配请求。
- apkctl.resolveEventCallback `0x1007c6190`：特殊 contextID `__autogo_lifecycle__` 按消息名查询生命周期回调；普通消息进入 SetCallback。
- 成功状态为 0，错误应答为 3。String 返回值也包含长度前缀。

这只证明已核实的帧格式与基础控制逻辑，不表示完整原生 UI 控制桥已经兼容。

## 仍存在的明确差距

1. OpenCV 的完整公开 Go 绑定尚未接入；现有识别胶水不能算完整 OpenCV SDK。
2. ImGui 的完整生成绑定尚未接入；样本包含约 5465 个公开符号（含大量结构访问器），现有小接口不能算全量。
3. agoverlayd/floatball 的全局窗口托管、触摸 socket、跨应用显示及生命周期尚未完成。当前 UIKit WindowScene 不能等价替代样本全局宿主。
4. 原生 JavaScriptCore 的 AGJSSelector/AGJSStruct/AGJSActionTarget 和 UI 构建协议尚未完整还原。
5. Yaegi 目前采用上游版本；样本内嵌 app/yaegi 分支差异、core 及第三方脚本绑定未全部核实和对齐。
6. AX 环境初始化与工作进程身份仍需在已有授权的真机环境核对；不在并发 Go 宿主里临时切进程级 euid/egid。
7. 输入、截图、视频、AX、OCR/YOLO、方向与多指虽已有实现，仍需同环境真机行为对照；模拟器截图之前返回 render server 错误 1。
8. iOS 最低构建版本目前仍为 15，样本为 13；未实测前不能仅改 Info.plist 宣称支持 iOS 13。

所有差距继续保留，不用接口数量或编译成功冒充完整一致。

## 验证

本地已检查 SQLite 实际文件头与第二连接可见性、二进制 Plist 嵌套值、TCP 帧/回执/事件/关闭、Go/ZIP 文件入口、远端取消和缓存保留、Go 静态检查。Apple 构建与模拟器结果在 CI 完成后补入本节。没有真实 iPhone 验收。

## 开源依赖

- SQLite iOS 驱动：github.com/mattn/go-sqlite3 v1.14.27（MIT，许可附于 third_party/licenses）。
- Plist：howett.net/plist v1.0.1（MIT，许可同上）。
- 不使用 cimgui-go 最新版本直接替代样本绑定：现有上游新版本已到 ImGui 1.93，必须先核对样本 1.92.5 的 ABI 和对应生成器。

## 后续运行证据与版本差距

`ff984c8` 的 Apple 四项 CI 均通过，但从实际 app.log 发现 Runtime/app 安装后为 0644，script.run 因缺少执行位失败。因此这次绿色 CI 只能证明编译/打包/启动，不能作为脚本验收。样本宿主的 ARM64 调用点 `0x1000237b0`、`0x100023b4c`、`0x100023cd8`、`0x100025218`、`0x100027908`、`0x100027b20` 均存在 chmod，mode=0x1ed（0755）；已补齐执行体的这一准备步骤。

本轮进一步将打包的 Runtime/app 改为 Foundation/CoreFoundation 的原生薄启动器，dlopen 宿主指定的同一份动态 SDK，调用 script.executeFile。SDK 不再同时被静态复制进任务二进制；整个任务期间与 SDK 安装互斥。同名已加载文件被更新替换后，要求先重启，避免旧宿主和新任务使用不同映像。Go cmd/runtimeapp 仍是开发用 CLI 示例，不是打包的工作进程。

已实际验证的上轮运行结果：宿主 JS 通道 `1+2=3`、界面点击计数 0→1；截图仍返回 render server 错误 1。修复执行位及原生启动器后的验收，以后续 CI 的 processId、解释器完成回执、JS 结果断言为准。全量探针已区分“调用返回”“环境失败”“仅参数拒绝”“未验证”，不能把参数校验成功视为功能通过。

样本原生库中的字符串进一步确认：OpenCV 4.10.0，ImGui `1.92.5 WIP (19244)`。旧构建下载了 4.12.0 的 OpenCV 头文件，现已锁回 4.10.0；当前 C RGBA 识别胶水不依赖 cv::Mat 的 C++ 布局。仓库现有 cimgui/ImGui 源码为 1.92.5 final（19250），仍不同于样本 19244；不能只改版本宏就宣称 ABI 一致。这项随完整 ImGui 生成绑定继续核实。上游 cimgui 的 1.92.5 WIP 提交也已到 19247，并不能直接当作样本原版。

[函数名静态清单](ios-autogo-function-inventory.json) 分别记录参考程序中可见的公开函数/方法名和源码名称。基础分组目前没有发现名字缺项，但没有验证全部签名/语义；OpenCV 有 428 个候选函数/方法名未覆盖，ImGui 有数千项候选名称未覆盖。core 为转发门面，也尚未提供相同导入路径。不能将此名字清单换算成可用率。

### 返回值如何理解

宿主 C ABI 的外层 ok 表示该调用是否形成有效响应；script.run 的 data.ok 表示解释器/工作进程是否执行完成；示例脚本 result.ok 表示它所检查的设备探针是否全部成功。例如模拟器截图失败时，解释器可以完整执行并返回 result.ok=false。CI 只验收其明确断言的进程与 JS 链路，不据此宣称截图、AX 或所有设备功能可用。

### 还不能视为精确源码一致的部分

样本 ReadPlist/WritePlist 调用的是内置 `github.com/Dasongzi1366/AutoGo/files/plist` 编解码器，本项目改用 howett.net/plist 提供同类完整格式支持，已经验证 XML、二进制和嵌套数据；该内置分支与上游库的全部差异尚未核对。类似地，SQLite 驱动版本采用公开 AutoGo 依赖中的 v1.14.27，并不宣称从闭源 iOS 构建信息中恢复了所有依赖版本。凡无法确认的分支/版本都继续登记，不写成完全相同。


执行体补充了独立看护：每秒检查宿主是否仍为父进程，宿主退出则结束任务；另外设置进程级硬期限，避免 SDK 内部原生调用阻塞后留下永久进程。进程终止仍不代表已经提交给系统的动作能够回滚。自检的 unsupported 明确表示当前构建/系统不支持，不再把模拟器缺少私有入口或未装识别库一律写成“源码未实现”。

## 最终本轮验证（97cae42）

构建记录：https://github.com/TuCoke/iosBuild/actions/runs/42975852051 。Go 检查、iPhoneOS SDK 编译、完整 App 打包、模拟器关键链路四项均通过。

模拟器产物中的 `script-check.json` 记录：processId=5874，解释器耗时 133ms，data.ok=true，result.interpreterCompleted=true，result.touchSubmitted=true。`js-check.json` 记录 JavaScriptCore 实际返回字符串 3。界面日志记录目标块点击计数 0→1。

示例的 result.captureSucceeded=false、result.effectVerified=false、result.ok=false，原因仍为 render server 错误 1；没有把这些字段改成成功。70 项探针的计数为：调用返回成功 37、环境失败 7、当前构建/环境不支持 2、跳过有副作用项 6、仅验证参数拒绝 18、未运行 0。此统计不是“70 项设备能力全部可用”。

本轮验证了正常进程完成和 JS 通道。父进程意外退出看护、进程硬期限已经实现并通过 Apple 编译，但没有单独注入杀宿主或不可中断原生阻塞来验收。SDK 安装后的真实手机重启加载、回滚，以及截图/视频/AX/识别等真机功能仍需真机验收。

最终代码同步到主仓库 `v2/sdk/ios` 及 `v2/ios` 构建镜像；SDK 源码比较未发现内容差异（忽略换行）。iOS 构建仓库验证分支为 `codex/ios-reference-runtime-20261111`。未将主仓库其他 Android、WDA 或本地内核的未提交改动混入验证分支。

## 2026-11-11 截图与 AX 专项定位

最新专项说明见 [截图与节点树失败定位](ios-capture-ax-diagnostics.md)。保持 AutoGo 的 CARenderServerRenderDisplay / IOSurface 和 AXRuntime / AXUIElement，不以外部 simctl 截图、XCUITest 或 UIKit 自身视图树替代 SDK 结果。

[95cdb4e 构建验证](https://github.com/TuCoke/iosBuild/actions/runs/42981319101) 四项检查通过。AX 原始异常为 `We're calling into AX runtime off the main thread!`；共享采集器统一经现有主线程边界执行后，界面进程取得 81 个真实节点，独立进程取得 82 个。CI 检查节点中存在界面上的“能力探测”按钮及有效坐标，未使用固定节点数量作为通过条件。

截图误判已定位并修复：真机样本以 0 继续读取，iOS 18.5 模拟器的同名外层函数却把有效内部应答转换成非零布尔值。此前把模拟器返回的 1 当成错误，导致在读取图像前退出。现在只在 iOS SDK 内按构建目标解释返回值，截图路线、对外 API 与 Agent 调用方式不变。

[9a1900d 最终验证](https://github.com/TuCoke/iosBuild/actions/runs/42987054926) 四项全部通过：宿主生成 JPEG 235×512 / 36782 字节，独立任务生成 235×512 / 38120 字节，已人工查看确认真实界面与方向正常。AX 为 79 / 80 个实际节点，真实按钮断言通过。脚本 PID 7949、258ms 完成，capture.json 返回 23318 字节，captureSucceeded=true、result.ok=true；effectVerified 仍未验证，不改成成功。

最终探针计数为调用成功 40、环境失败 4、不支持 2、跳过有副作用 6、仅参数拒绝 18、未运行 0。没有据此宣称所有 SDK 功能或真实 iPhone 已通过。专项验证分支为 `codex/ios-capture-ax-diagnostics-20261111`，完整调用点、对照实验和更正记录见专项说明。
