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
| 脚本入口 | GoMain → chdirToExecutableDir → main → runGoFile/runZip | Runtime/app 改为文件/ZIP 执行后退出；不再凭 8989 URL 猜测为常驻源码 HTTP 服务 |
| 进程隔离 | 壳包含 posix_spawn 等进程管理能力 | 宿主 script.run 使用独立工作进程，等待结束、超时终止、waitpid 回收；参数/结果文件为本项目明确自定义协议 |
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
