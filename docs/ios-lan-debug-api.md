# iOS 局域网调试接口

本接口属于验证宿主 AgovHost，独立于 SDK 的设备能力层。原 `127.0.0.1:18989` 保持不变。启动自检结束后自动监听 `0.0.0.0:18990`。App 被系统挂起或终止时不保证可达，测试时保持前台。

## 连接

1. 安装包含此改动的新 IPA，允许系统提示的“本地网络”访问。
2. 查看界面日志中的 `debugapi.start`：包含 `addresses` 和固定 `token`（`1234567`）。只有 `ok:true` 才表示监听成功；端口占用会明确失败。
3. 电脑与手机处于可互通的局域网，使用手机 IP；日志里的 `127.0.0.1` 不是手机的局域网地址。
4. Bonjour 广播 `_agovhost._tcp.local.`，只含协议版本，不包含 token。Mac 可用 `dns-sd -B _agovhost._tcp local.` 发现实例，再用 `dns-sd -L <实例名> _agovhost._tcp local.` 查询主机/端口。Windows 需要 Bonjour 客户端；没有客户端时直接使用日志中的 IP。广播失败会写日志，不能把广播配置成功当成已经在网络中发现设备。

调试 token 按当前约定固定为 `1234567`，重新启动不会改变。优先使用 `Authorization: Bearer <token>`；兼容用户需要的 `?token=<token>`。日志包含访问凭据，不要公开分享仍在运行设备的完整日志。此服务使用局域网 HTTP，不应直接暴露到公网。

```powershell
$phone = 'http://192.168.1.100:18990'
$headers = @{ Authorization = 'Bearer 1234567' }
Invoke-RestMethod "$phone/status" -Headers $headers
Invoke-RestMethod "$phone/selftest" -Headers $headers
Invoke-WebRequest "$phone/log" -Headers $headers -OutFile './iphone-app.log'
Invoke-RestMethod "$phone/diagnostics/worker" -Headers $headers
Invoke-RestMethod "$phone/script/test" -Method Post -Headers $headers
$body = '{"method":"motion.tap","args":{"x":0.5,"y":0.5}}'
Invoke-RestMethod "$phone/invoke" -Method Post -Headers $headers -ContentType 'application/json' -Body $body
```

## 接口

所有接口均需鉴权，包括健康检查和接口目录。所有返回禁止缓存，不开放 CORS，带 Origin 的网页请求返回 403。命令行调用和浏览器直接打开地址可用；不提供公开跨源控制入口。

| 方法/路径 | 内容 |
| --- | --- |
| GET `/api` 或 `/` | 实际接口目录、允许调用的方法、协议版本 |
| GET `/health` | 调试服务 PID、存活时长、调试动作忙状态；不访问设备原生能力 |
| GET `/status` | SDK 实际设备信息；失败仍返回真实错误 |
| GET `/log` | `Documents/app.log` 的完整打开时快照，支持 HTTP Range；不能指定其他文件 |
| GET `/capabilities` | 能力声明；声明可用不代表已验证动作效果 |
| GET `/selftest` | 触发既有全量自检，返回真实分类结果 |
| GET `/diagnostics` | 宿主截图、AX 和运行环境完整报告 |
| GET `/diagnostics/worker` | 启动独立 worker，返回 PID、耗时、截图、AX、环境或完整错误 |
| GET `/screenshot` | 截图 JSON，含 MIME、尺寸和 base64 |
| GET `/ui-tree` | 当前控件树 |
| GET `/sdk` | 当前 SDK 路径、摘要、版本/更新状态 |
| GET `/resources` | 资源完整性与可用情况，默认不额外遍历计算所有摘要 |
| GET `/worker` | 独立工作进程状态；其他动作执行中仍可查询 |
| GET `/tunnel` | 现有穿透进程状态，不会自动启动穿透 |
| GET `/results` | 本次服务启动后每个调试路径的最近完整回执；内存中固定数量覆盖保存，重启不保留 |
| POST `/script/test` | 仅运行内置演示脚本；请求体为空或 `{}`，不允许上传源码/本地路径 |
| POST `/invoke` | `{"method":"白名单方法","args":{}}`；返回 SDK 原始 JSON 回执 |

`GET /selftest` 按本次需求保留，但它有副作用：可能点击界面、改剪贴板、创建/销毁悬浮层。不要在处理真实业务时调用。重启、卸载、清数据等破坏性功能不经此调试接口开放；因此“全量自检”仍不等于每项能力成功，也不等于业务效果验证成功。

`/invoke` 的准确白名单以 `/api` 的 `allowedMethods` 为准，覆盖应用读取/启动、输入、手势、亮度、系统按键等测试。不开通任意命令、进程启动/终止、任意脚本下载/执行、任意文件读写、SDK 安装或升级。后续扩展方法需在宿主调试白名单内明确登记，不应放开任意前缀。

## 并发与错误

- 一次只运行一个调试动作。与宿主界面调用共用互斥锁，冲突明确报忙，不悄悄排队。
- 请求断开或超时不能强行中断所有原生代码；尚未返回的调用仍持有互斥锁，不允许提前释放后再并发操作。
- `/health`、`/log`、`/results` 不依赖动作锁；`/worker` 仅查询 worker 自己保护的状态。
- 调试调用上下文最长 120 秒，独立 worker 保留自身的硬超时/父进程看护。HTTP 超时不代表手机动作已回滚。
- HTTP 401 鉴权失败；403 跨源或白名单拒绝；404 未知路径/日志不存在；405 方法不符；409 任务忙；502 SDK 非法 JSON。
- 原生能力错误保留 SDK 的 `ok:false/error`。脚本还需检查 `data.ok`，不能只看 HTTP 200。
- `/invoke` 请求体最多 64 KiB；保留既有 SDK 参数校验。固定日志路径拒绝软链接，不接受客户端传任意路径。

## 验证范围

新增纯 Go 测试覆盖鉴权/固定 token、跨源拒绝、路由分派、危险方法拒绝、请求大小与 JSON 校验、内置脚本、并发/取消、日志/回执读取、非法 SDK 响应、日志软链接。Windows 无软链接权限时该项跳过，Linux CI 执行。

GitHub 模拟器增加真实 HTTP 冒烟：读取 App 日志内 token，以请求头鉴权测试设备状态、自检、worker 诊断、截图、节点和内置脚本，结果写入 `Documents/debugapi-smoke.json`。单元测试和模拟器成功不能替代真机局域网连通性、Bonjour 发现及动作效果验证。

## 本轮构建验收（2026-11-11）

- 主仓库功能提交 `25e9a05`；独立 GitHub 构建提交 `9371300`。
- [本轮 Actions](https://github.com/TuCoke/iosBuild/actions/runs/43042316464) 四项全部成功：Go 检查、iOS SDK 原生编译、宿主 IPA/DEB 打包、模拟器运行与调试 HTTP 冒烟。
- 已读取本轮 `debugapi-smoke.json`：未授权请求 HTTP 401；health、status、api、capabilities、worker、selftest、worker diagnostics、screenshot、ui-tree 和内置脚本共 10 项实际 HTTP 检查通过；日志读取也已执行。
- 本机 Windows 的新测试通过；日志软链接用例因系统权限跳过，由 Linux CI 补验。静态检查、vet、Python 和 plist 语法检查通过。
- 本轮模拟器验证不包含真机局域网连接、Bonjour 自动发现或所有动作的界面效果；真机需要安装新包后单独验收。

## 固定鉴权与防息屏补充

按用户要求，局域网调试 token 固定为 `1234567`。`Authorization: Bearer 1234567` 和 `?token=1234567` 均可；错误或缺少 token 仍然拒绝。宿主在主线程设置 `idleTimerDisabled=YES`，前台运行时防止自动息屏；这不阻止手动锁屏，也不提供后台常驻。全量自检不再执行 `device.keep_awake=false`，该项归为跳过有副作用，避免覆盖宿主保活设置。上述改动需安装新包生效，前一节 CI 结果对应此前的功能提交。

## 新增：连续视频和完整设备调试入口（2026-11-12）

此版本需重新构建并安装 IPA。仅有旧版 /selftest 返回成功，不代表这些新接口已部署。

### 视频接口

所有请求继续携带 `Authorization: Bearer 1234567`，也兼容 `?token=1234567`。

| 方法/路径 | 用途 |
| --- | --- |
| POST /video/start | 开始后台限时采集；读取并统计实际编码帧，不依赖 HTTP 启动请求一直保持 |
| GET /video/status | running、frames、bytes、keyframes、实际尺寸、PTS、平均 FPS/码率、最近帧年龄与错误 |
| GET /video/packet | 最新完整访问单元 JSON；data 是 Base64，sequence 用于判断是否重复读取 |
| POST /video/keyframe | 请求新的 IDR |
| POST /video/bitrate | 运行中调整码率，body 为 {"bitrate":500000} |
| POST /video/stop | 取消采集并等待编码器释放；清理失败如实报错并禁止新会话 |
| GET /video/stream | 独占一次限时采集，连续返回原始 Annex B H.264，结束或断开时停止采集 |

`/invoke` 同时支持 video.start/read/status/keyframe/bitrate/stop，映射到相同的有界视频管理器，不能传 native token 抢占其他流。video.read 是最新帧快照，并非连续无损消费队列。视频读取不占用普通设备操作的 HTTP 闸门，状态和停止可以在下载中调用。

start 请求使用平铺的 SDK 配置：maxSize、fps、bitrate、queueBytes、autoRotation、rotation，以及 durationSeconds。默认最长边 960、15 FPS、1.5 Mbps、1 MiB SDK 队列、自动旋转、30 秒；调试时长范围 1..120 秒，SDK 缓冲不超过 4 MiB，未知参数拒绝。一个进程仅允许一个调试视频所有者；已经 start 时不能再 stream，请先 stop。只保留最新帧和最多四个下载待发帧，单帧不超过 4 MiB，不将视频 Base64 存入 /results。

连续下载从首个关键帧开始，保留 SDK 提供的 SPS/PPS；慢下载不会无限积压或随机丢参考帧后继续输出，队列发送阻塞超过 2 秒时结束采集，网络单次写入设置短超时，原因可查 status。HTTP 已开始输出后无法再改成 JSON 错误，需同时查看 status.error。连续下载不是 MP4，也不是 WebRTC 浏览器播放器；浏览器会下载 .h264 文件，可以用支持 H.264 的播放器查看。

```powershell
$phone = 'http://49.235.187.13:18990'
$headers = @{ Authorization = 'Bearer 1234567' }
$body = '{"durationSeconds":30,"maxSize":720,"fps":15,"bitrate":800000}'
Invoke-RestMethod "$phone/video/start" -Method Post -Headers $headers -ContentType 'application/json' -Body $body
Invoke-RestMethod "$phone/video/status" -Headers $headers
Invoke-RestMethod "$phone/video/packet" -Headers $headers
Invoke-RestMethod "$phone/video/keyframe" -Method Post -Headers $headers
Invoke-RestMethod "$phone/video/bitrate" -Method Post -Headers $headers -ContentType 'application/json' -Body '{"bitrate":500000}'
Invoke-RestMethod "$phone/video/stop" -Method Post -Headers $headers

# 独立抓取 5 秒连续视频，运行前先停止上面的手动会话。
Invoke-WebRequest "$phone/video/stream?durationSeconds=5&maxSize=720&fps=15&bitrate=800000" -Headers $headers -OutFile './ios-debug.h264'
```

### 其他补齐的调试能力

`/api.allowedMethods` 为实际清单；均通过 POST /invoke 调用，不开放任意前缀。

- 手势：motion.gesture 完整轨迹、motion.pinch 双指缩放、pointer、status，保留点击/长按/滑动/frame。
- 输入：ime.text、ime.key、ime.segments；界面效果需自行确认。
- 应用：app.list_all、app.stop，保留应用读取/启动；app.install 和 app.uninstall 不开放。
- 观测：capture.screenshot/json/release、uiacc.snapshot/warmup/prepare/restore、设备 Wi-Fi MAC 查询。
- 显示：HUD 的 show/visible/hide/destroy，console 的 show/print/clear/hide/visible/destroy，以及 toast/alert/input_alert。弹窗可能等待用户操作，不应作为自动冒烟的一部分。
- 识别：ppocr.load/ocr/close，yolo.load/set_image/detect/detect_image/close，dotocr.set_dict/ocr/find。
- 图像：opencv.decode/rgba/info/encode/match/clone/gray/crop/resize/rotate/threshold/blur/canny/close。

识别模型、Mat 和悬浮层的 token/id 依照对应 SDK 协议传递，使用后主动 close/destroy；接口开放不代表模型已经交付或识别结果已验证。/invoke 请求上限从 64 KiB 调整为 6 MiB，以便提交 Base64 图片，SDK 自身的尺寸/模型数量限制继续生效。宿主通用调用保留数组/字符串等 JSON 原始类型，修复应用列表等结果被强制当成对象的问题。

双指示例：

```json
{"method":"motion.pinch","args":{"a0":{"x":0.4,"y":0.5},"b0":{"x":0.6,"y":0.5},"a1":{"x":0.2,"y":0.5},"b1":{"x":0.8,"y":0.5},"durationMs":600}}
```

安装/卸载按本次要求不开放。原有任意 shell/进程启动、清数据、设备重启、任意远程脚本和 SDK 安装限制保持不变，这些不属于本轮视频及日常能力调试入口。

### 验证范围

纯 Go 测试验证真实 HTTP 路由/鉴权、二进制内容输出、关键帧起步、启动互斥、参数、控制下发、限时退出、断开清理和清理失败重试。测试采集器是替身，不算真机编码通过。

GitHub 模拟器增加 /video/status 检查并实际请求两秒 H.264；debugapi-smoke.json 的 videoCaptureProbe 记录样本字节数、SPS/PPS/IDR NAL 类型或真实失败原因，debug-video.h264 随模拟器产物上传。模拟器硬件能力失败单独登记，不冒充成功；最终仍需真机下载样本、解码和连续运行验收。

## 后台运行补充

本轮新增统一宿主后台运行需求管理，覆盖调试 HTTP、视频、worker、JS 控制连接及隧道。详见 [iOS 后台运行与脚本生命周期梳理](proposals/ios-background-runtime.md)。GET /background 查询需求、实际音频运行、前后台和错误；background.enable 控制策略。需安装新包，后台和长稳效果仍需真机验收。


## 2026-11-12：切换 App 时的视频编码恢复

真机复现：AgovHost 前台推流后打开设置，出现“视频像素缓冲分配失败”或提交编码返回 -12903；与此同时宿主 PID、后台音频和 HTTP 接口仍正常。稳定后台重新开启视频能够工作，问题发生在现有编码链路跨越 App 切换时，不能直接归因于 FRP 或整个宿主被暂停。

本轮修改只在 SDK 视频原生层，源码 `v2/sdk/ios/internal/native/video_ios.m` 与独立构建镜像 `v2/ios/sdk/ios/internal/native/video_ios.m` 保持一致：

- 缓冲池不存在或分配失败时，回退到独立 BGRA 像素缓冲；日志区分 pool 与 fallback 返回码。
- 截图、像素缓冲和画面转换的单帧失败跳过本轮；连续 10 次失败才触发重建。
- 编码提交或回调报错、连续两秒无编码输出时，内部重建 VideoToolbox 会话，不立即结束 SDK 句柄或 HTTP 流。
- 重建退避为 250/500/1000/2000/2000 ms，最多 5 次；真正连续稳定输出 10 秒才重置次数。持续失败最终返回明确错误，不能无限空跑。
- VideoToolbox 回调统一投递到串行编码队列，样本保留到消费后释放；用代次丢弃旧会话回调。每次恢复清空原生待发包，等待带 SPS/PPS 的新 IDR，PTS 沿用最初启动的单调时钟。
- 恢复中修改码率会保留最新值供重建使用；停止会取消采集定时器并使旧回调失效，不通过 sleep 阻塞停止。

日志关键字：`[video] 缓冲池暂不可用`、`跳过失败帧`、`等待恢复`、`编码器已重建`、`编码恢复`。重建成功只表示会话创建完成；必须看到真实编码帧后才记录“编码恢复”。

AutoGo 样本确认提供缓冲池失败后的 CVPixelBufferCreate 回退、失败帧继续循环以及输出尺寸变化重建；不能据此声称样本有 -12903 自动恢复。本轮的有限次数错误恢复是我方明确增加的容错。

验收须使用本轮重新编译安装的包：同一条长视频流中反复执行 AgovHost → 设置 → AgovHost，检查帧数恢复递增、HTTP 未因瞬态错误结束、恢复首帧可解码，并覆盖恢复中停止、修改码率和最终失败释放。旧安装包不包含本轮改动；Windows 的静态检查不能替代 Apple 原生编译及真机验收。视频接口原有 durationSeconds 到期仍会正常结束，不属于编码故障。本轮未改变自动旋转或 HTML 自动重连策略。

本轮验证记录：本地 precheck 全部通过（108 个 Go 文件、26 个原生/包装层文件），Python 回归脚本语法检查与双份 SDK 源码一致性检查通过。`v2/ios/tools/debugapi_smoke.py` 已增加同一编码会话跨设置/宿主切换的出帧检查：基础编码可用时，切换后八秒内必须继续出帧，否则 CI 失败；基础编码不可用则明确记录跳过。此检查尚未在 Apple 环境执行，也尚未用新包复测真机，不能登记为运行验收通过。


## 视频 WebSocket / FRP 独立接入

新增 `GET /video/ws` 升级接口，和现有 18990 HTTP 调试端口并存。FRP 保持 TCP 映射到 127.0.0.1:18990，无须新增端口；浏览器直接连接公网映射的 WS/WSS，视频不经过本地 Python 转发。

鉴权在升级后的首消息中完成，5 秒内发送 `{"type":"start","token":"调试口令","options":{"durationSeconds":60,"maxSize":720,"fps":15,"bitrate":800000}}`；成功返回 `started`，失败返回 `error`，鉴权失败不会申请编码器。允许同源页面和固定本机实验台来源，HTTP 接口仍拒绝跨源；WS 地址不携带 token。

每条二进制消息是 AGV1 40 字节大端帧头加一个完整 Annex B 访问单元：magic、关键帧标记、序号、PTS 微秒、宽高、负载长度、配置版本。SPS/PPS 或尺寸变化会在关键帧上更新配置版本；浏览器据此重置解码器。普通数据包不是 Base64，也不采用网络分块猜帧或固定 FPS 猜时间戳。

同一 WS 连接接受 `keyframe`、`bitrate`、`stop` 视频控制。视频与设备操作使用独立连接；编码源、所有权、有界缓冲和限时规则复用现有实现，不增加第二个编码器。连接断开释放本连接的视频；重复连接拒绝，不抢停现有会话。服务关闭显式关闭升级连接，因为 net/http.Shutdown 不负责 WebSocket。正常采集结束发送 `ended`，附带真实编码错误；心跳 5 秒、Pong 超时 15 秒、单次写入超时 3 秒。

页面与协议细节见 `v2/tools/diagnostics/ios-web-test/README.md`。本机 Python 仍用于提供页面、截图和设备操作转发，只有视频改成浏览器直连；这不等于已经接入正式 Agent 或实现多租户云控鉴权。现有调试时长仍限制 1..120 秒。

本轮本地验证：Go 调试 API 测试、go vet、precheck、JS 语法及 3 项 H.264/帧头 Node 测试通过。Go 视频测试使用替代源，证明 WS 协议与清理，不证明真实 iOS 编码。Apple 编译、重新安装、FRP 浏览器播放及跨 App 真机复验尚未执行；旧手机安装包没有新接口。
