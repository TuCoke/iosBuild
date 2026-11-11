# iOS 局域网调试接口

本接口属于验证宿主 AgovHost，独立于 SDK 的设备能力层。原 `127.0.0.1:18989` 保持不变。启动自检结束后自动监听 `0.0.0.0:18990`。App 被系统挂起或终止时不保证可达，测试时保持前台。

## 连接

1. 安装包含此改动的新 IPA，允许系统提示的“本地网络”访问。
2. 查看界面日志中的 `debugapi.start`：包含 `addresses` 和随机 `token`。只有 `ok:true` 才表示监听成功；端口占用会明确失败。
3. 电脑与手机处于可互通的局域网，使用手机 IP；日志里的 `127.0.0.1` 不是手机的局域网地址。
4. Bonjour 广播 `_agovhost._tcp.local.`，只含协议版本，不包含 token。Mac 可用 `dns-sd -B _agovhost._tcp local.` 发现实例，再用 `dns-sd -L <实例名> _agovhost._tcp local.` 查询主机/端口。Windows 需要 Bonjour 客户端；没有客户端时直接使用日志中的 IP。广播失败会写日志，不能把广播配置成功当成已经在网络中发现设备。

每次进程重新启动生成新的 32 字节随机 token。优先使用 `Authorization: Bearer <token>`；兼容用户需要的 `?token=<token>`。日志包含访问凭据，不要公开分享仍在运行设备的完整日志。此服务使用局域网 HTTP，不应直接暴露到公网。

```powershell
$phone = 'http://192.168.1.100:18990'
$headers = @{ Authorization = 'Bearer 替换为日志中的token' }
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

新增纯 Go 测试覆盖鉴权/随机 token、跨源拒绝、路由分派、危险方法拒绝、请求大小与 JSON 校验、内置脚本、并发/取消、日志/回执读取、非法 SDK 响应、日志软链接。Windows 无软链接权限时该项跳过，Linux CI 执行。

GitHub 模拟器增加真实 HTTP 冒烟：读取 App 日志内 token，以请求头鉴权测试设备状态、自检、worker 诊断、截图、节点和内置脚本，结果写入 `Documents/debugapi-smoke.json`。单元测试和模拟器成功不能替代真机局域网连通性、Bonjour 发现及动作效果验证。

## 本轮构建验收（2026-11-11）

- 主仓库功能提交 `25e9a05`；独立 GitHub 构建提交 `9371300`。
- [本轮 Actions](https://github.com/TuCoke/iosBuild/actions/runs/43042316464) 四项全部成功：Go 检查、iOS SDK 原生编译、宿主 IPA/DEB 打包、模拟器运行与调试 HTTP 冒烟。
- 已读取本轮 `debugapi-smoke.json`：未授权请求 HTTP 401；health、status、api、capabilities、worker、selftest、worker diagnostics、screenshot、ui-tree 和内置脚本共 10 项实际 HTTP 检查通过；日志读取也已执行。
- 本机 Windows 的新测试通过；日志软链接用例因系统权限跳过，由 Linux CI 补验。静态检查、vet、Python 和 plist 语法检查通过。
- 本轮模拟器验证不包含真机局域网连接、Bonjour 自动发现或所有动作的界面效果；真机需要安装新包后单独验收。
