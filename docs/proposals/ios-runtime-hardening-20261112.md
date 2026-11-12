# iOS 运行稳定性：穿透、方向与跨进程设备使用权

日期：2026-11-12。本轮修复三个优先问题，不改 Agent、MCP、安卓或云端 Session 协议。

## 1. 穿透生命周期

- 相同配置重复启动复用已确认存活的 frpc，不覆盖 PID、不创建第二个进程。
- 运行中的配置变化明确要求先停止；无法确认旧进程状态时拒绝创建新进程。
- 生成官方配置 loginFailExit=false，首次联网未就绪时由 frpc 重试。进程运行不等于映射已注册或公网一定可用。
- 宿主看护进程退出，2 秒检查，恢复按 2/4/8/16 秒退避，最多连续 5 次；稳定一分钟才重置次数。网络重连由 frpc 负责，这个次数限制只约束宿主的进程恢复。
- Stop 撤销看护需求，不能停止后又被重启。状态提供 supervising、restartAttempts、restartError；界面显示恢复中和最终停止原因。
- 恢复期间保留 tunnel 后台运行需求；宿主被系统终止后，此进程内看护不再运行，不能宣称能够自行复活整个 App。

涉及 sdk/ios/tunnel、cmd/ioshost/main.go、host/ios/Sources/AGRootViewController.m。

## 2. 视频方向跟随

- autoRotation=true 时逐轮读取 FrontBoard 已发布的方向；方向变化后废弃旧回调和待发包，更新输出几何，重建编码器并请求新关键帧。
- 正常方向切换不消耗错误重建次数；已经存在的错误恢复预算不会被旋转清零。
- PTS 沿用启动时的单调时钟，每个队列包保存自己的宽高，防止旧包与新尺寸混用。现有 WS 根据关键帧中的尺寸和 SPS/PPS 更新配置版本。
- autoRotation=false 保持调用方指定方向。

涉及 sdk/ios/internal/native/video_ios.m。该设计仍需真机覆盖四个方向、切 App 时旋转和恢复期间旋转。

## 3. 跨进程设备使用权

- 宿主与 worker 使用宿主 Documents 下同一个 .agov-device-input.lock；worker 启动显式传入 --documents，不能按独立进程的 HOME 推导另一把锁。
- 原生层使用非阻塞 flock。锁文件不删除，避免 inode 变化形成两把锁；描述符带 O_CLOEXEC，不继承给 frpc。协作进程退出后由内核释放。
- 高层手势和输入在完整动作期间持有引用；宿主下发的设备脚本通过 Options.DeviceExclusive 在完整运行期间持有租约，包括观察和等待阶段。纯计算脚本默认仍可并发。
- 原生 motion/ime、应用写操作、设备写操作共用同一锁边界，C bridge 和 JSON 原生调用不能绕过该边界。读取状态、截图、视频不因此被独占任务阻止。
- 同进程允许嵌套引用，保留高层动作串行闸门；另一进程得到明确的忙错误，不无限排队。显式取消不能阻止释放已经取得的引用。
- 这是同一 SDK 宿主/worker 的本机协作机制，不替代云端多租户授权、Session 或第三方 App 的协调。已提交的系统动作不能因此回滚。低层逐指接口仍须由调用方组织完整动作生命周期。

涉及 sdk/ios/internal/inputstate、internal/native/input_lease_ios.m、app/device/touch 原生入口、script.Options、script/worker 和宿主 script.executeFile。

必须安装包含本轮宿主和 SDK 的完整新包；仅替换动态库、继续使用不传 --documents 的旧 worker，不能保证共享同一个目录。

## 验证与边界

本地定向 Go 测试通过：输入闸门/任务嵌套及释放、相同配置重复启动、配置变更拒绝、退出后恢复尝试、显式停止取消恢复、纯计算脚本并发、独立脚本准备。go vet 与 precheck 通过；SDK 与独立构建镜像同步。

Go 进程测试使用替代原生调用，不代表真实 iOS flock、后台重启或旋转已通过。Apple 原生编译及新包真机验收结果应单独记录，不能沿用上一版 WS 视频播放成功的结论。

本轮没有修复原始 BGRA 帧采集的单次失败停止、AX 旧节点重新校验、更新的长期健康/发布信任链、强杀手势后的系统触点核对或完整跨 App 悬浮宿主。这些保持明确待办，不登记为已完成。
