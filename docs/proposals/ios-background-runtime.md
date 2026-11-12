# iOS 后台运行与脚本生命周期梳理

日期：2026-11-12。范围：AgovHost、调试视频、宿主 JS 通道、脚本 worker、frpc，以及后续云控 Agent 的接入边界。本轮没有改变 Android Agent 或服务端执行框架。

## 1. 问题与证据

当前视频采集和 HTTP 服务运行在 AgovHost 进程，切出 App 后受宿主后台调度影响。HTTP 连接仍建立、frpc PID 存在、线程未主动退出，都不能证明进程仍在持续执行。

本地 AutoGoRunner 样本反汇编确认：

- `AGInstallDebugAudioKeepAlive` 由调试启停和运行状态更新调用。
- `AGStartDebugAudioKeepAlive` 调用 `AGCreateSilentWAVData`、AVAudioSession 设置/激活、AVAudioPlayer 初始化和播放。
- `setNumberOfLoops:` 的参数是 -1，`setVolume:` 参数是 0；不是仅发现字符串就推断逻辑。
- 样本 Info.plist 声明 audio 等后台模式，注册前后台、音频中断及音频服务重置事件。
- 脚本执行体和 frpc 独立 spawn；`BypassJetsam` 在已检查的调用关系中用于悬浮进程加固，不能推断所有进程都获得永久保护。
- 本项目已有部分相同 entitlement 声明，但此前缺少实际后台音频及生命周期管理逻辑。

普通后台任务提供有限执行时间，不是永久运行保证。系统授权、目标 iOS 版本、音频中断和实际签名仍需真机验证。官方说明：https://developer.apple.com/documentation/BackgroundTasks/choosing-background-strategies-for-your-app

## 2. 本轮实现

新增 `AGBackgroundRuntime`，统一管理宿主运行需求，而不是让视频、脚本、frpc 各自播放音频。由宿主 AppDelegate 安装，独立 SDK worker 没有该组件时明确返回 available=false。

运行需求使用稳定 owner 幂等登记：

| owner | 获取时机 | 释放时机 |
| --- | --- | --- |
| debug-api | debugapi.start | 调试服务确认关闭或启动失败 |
| local-api | localapi.start | localapi.stop 确认关闭或启动失败 |
| tunnel | tunnel.start | tunnel.stop 成功，或状态查询确认进程已退出 |
| debug-video | 视频采集器实际启动 | 编码器确认关闭；失败时保留需求供重试 |
| script-worker | Go/ZIP/远程脚本和独立 worker 诊断开始前 | 父进程确认 worker 不再运行；结果未知时不提前释放 |
| js-connection:UUID | 宿主 JS 控制连接开始处理 | 连接断开后，JS 队列当前执行和上下文清理结束 |

需求登记和音频实际运行分开：需求集合同步更新，AVAudioSession/AVAudioPlayer 操作只在 UIKit 主线程执行。重复启动幂等，重复启动失败不会释放原服务已持有的需求。

存在需求且启用后台策略时：创建自维护静音 WAV、Playback 音频会话、MixWithOthers、音量 0、无限循环。没有需求或显式禁用时停止播放器并尝试释放音频会话。音频启动失败不会让调试服务伪称消失，但状态和日志明确报告失败，不把 requested=true 当作 audioPlaying=true。

配置只新增实际用到的 UIBackgroundModes=audio；不照抄样本整组定位、蓝牙、VoIP 等声明。未实现系统 Jetsam 规避、内核绕过或永久守护保证。

前后台事件、音频中断、允许恢复和音频服务重置均登记日志；系统不允许后台恢复时保留中断状态，等待前台或人工重试，不用无界重试循环。生命周期 pause/resume 事件接入既有宿主 JS 通道，串行后台队列发送，不在 UIKit 主线程等待客户端 socket。

## 3. 哪些脚本会受影响

| 执行环境 | 之前的风险 | 本轮处理 | 尚需验证 |
| --- | --- | --- | --- |
| iOS 宿主 JavaScriptCore | 宿主挂起会冻结 JS、UI 桥及消息应答 | 按 JS 连接登记需求，断开后等执行清理完再释放 | 切 App、Promise 应答、音频中断及长稳 |
| iOS Go/ZIP worker | 子进程可能继续，但父进程收结果/超时处理/JS 宿主桥会暂停 | 父进程运行需求覆盖整次 worker，清理未知时保留 | 子进程自身后台调度与真实设备动作 |
| iOS 视频/HTTP | 采集、编码读取、HTTP 同在宿主，可能一起暂停 | 视频和服务分别登记；状态、停止可以独立访问 | 跨 App 连续视频、锁屏、受保护画面、弱网 |
| frpc 子进程 | frpc 活着也可能转发到已暂停的宿主 | 隧道与 HTTP 服务各有需求，不能只观察 frpc PID | 切 App 后端到端 HTTP 与视频，而非仅登录成功 |
| 云端固定 JS/AI 工作流 | 服务器进程不随手机 App 后台变化，但设备调用会超时 | 不改服务器脚本执行器；继续保留明确结果语义 | 设备离线/恢复与任务队列的结果核对 |
| 电脑上的 Python/JS/MCP | 电脑端可继续，手机端仍可能不可用 | 不改变外部 SDK 协议 | 超时不代表动作未执行，禁止盲重试发布/输入等 |
| Android Agent | 不使用 iOS UIKit/AVAudioSession | 本轮不修改 | 保持既有行为 |

现有 RuntimeMain.m 已有主线程 CFRunLoop、进程级硬期限，以及宿主退出后终止 worker 的检查。getppid 检查不能检测“父进程仍活着但被挂起”，因此不能用它替代宿主后台管理。

音频策略保护的是宿主的运行机会，不会自动授予子进程永久后台权限，也不会解决私有 API、内容保护或 App 本身的交互限制。服务端 Session 是业务执行归属；这里的 owner 是本机资源运行需求，两者不可混为同一把设备锁。

## 4. 调试与日志

新增 GET /background，以及 POST /invoke 的 background.status/background.enable、localapi.stop。状态只读取自身锁，普通动作忙时仍可查询；修改后台策略遵循既有动作闸门。

```powershell
$phone = 'http://49.235.187.13:18990'
$headers = @{ Authorization = 'Bearer 1234567' }
Invoke-RestMethod "$phone/background" -Headers $headers

# 明确关闭/重新启用后台音频策略；运行需求不因此丢失。
Invoke-RestMethod "$phone/invoke" -Method Post -Headers $headers -ContentType 'application/json' -Body '{"method":"background.enable","args":{"enabled":false}}'
Invoke-RestMethod "$phone/invoke" -Method Post -Headers $headers -ContentType 'application/json' -Body '{"method":"background.enable","args":{"enabled":true}}'
```

返回 available、enabled、requested、owners、audioPlaying、audioSessionActive、interrupted、appState、lastError、lastEventAt。guaranteedBackgroundExecution 固定 false，避免错误承诺。启动初期主线程尚未执行音频操作时，可能先看到需求已登记、播放器尚未启动，稍后重新查询。

日志统一追加到 Documents/app.log，使用 `[background]` 前缀：需求变更、前后台、音频失败/中断/恢复。只记录 owner 与运行状态，不记录凭据或脚本正文。

宿主目前自动开启 local-api 和 debug-api，所以默认一直存在服务需求；关闭视频或完成一份脚本后，音频不会因此停止。如果需要完全关闭，请停止其他服务，或显式 background.enable=false。这不是计数泄漏。

## 5. 框架边界与仍需做的事

正确分层：ObjC 宿主管理 UIKit/AVAudioSession 生命周期；Go 装配入口登记服务需求；SDK 只负责设备能力；worker 负责隔离任务和硬期限；云端 Session/队列负责业务串行、归属与结果核对。

本轮不会通过新增业务锁改动核心框架。后续 iOS Agent 应接入同一运行需求管理边界，而不是另建音频播放器。若转为独立常驻 Agent 执行体，需要再次明确其系统身份、事件循环、启动方式、父子进程看护和升级时任务策略；本轮没有把生产 Agent 守护进程算作完成。

仍需真机验收或设计闭环：

1. 切到其他 App 连续 30 分钟，分别检查视频帧、HTTP、JS 与 worker 回执。
2. 来电、音频播放、耳机变化、音频服务重置、锁屏后记录是否暂停及恢复，不仅看 API 请求成功。
3. 父进程被系统终止时，由服务端按离线/未知结果处理；iOS 不保证 applicationWillTerminate 回调。
4. 异常退出时 owner 仅在本进程内存中，重启后由真实服务重新登记，不持久化一组失效租约。
5. 单独核对子进程的权限与调度；独立进程、TrollStore、entitlement 和线程存在都不是常驻证明。
6. 未来云控 Agent 对重连、不可重试动作、journal 与多租户的规则继续使用既有框架，不能用保活代替恢复设计。

## 6. 验证边界

Windows 可以运行 HTTP/生命周期接口的纯 Go 路由测试、语法/静态检查和 plist/Python 校验，不能本地验证 AVAudioSession 的真实效果。

GitHub 构建会编译 ObjC 组件与 Cgo 桥，模拟器核对后台管理器和 debug/local 服务需求，并切到设置 App 三秒后查询 HTTP/后台状态、再回到宿主。报告与 `[background]` 日志随产物保存；三秒模拟器探测不是三十分钟真机后台验收。

完成本轮代码后必须安装新 IPA 才生效。本地原始 H.264 的前台解码成功证据继续有效，但不能据此推断此次后台修复已经通过真机。
