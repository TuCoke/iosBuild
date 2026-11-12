# 独立 iOS Agent

状态（2026-11-12）：代码接入与桌面协议测试已完成；尚未经过本轮 Apple 编译、IPA 安装和原生 Agent 真机验收。此前真机结果来自电脑 iosremote 桥，不能代替本目录原生入口验收。

## 边界

- `agent/ios/runtime`：iOS 专属注册、连接、命令分路、持久回执、取消、心跳及视频生命周期。
- `agent/ios/commands`：公共命令到 iOS SDK 的映射，通过自己的 Backend 接口装配。
- `agent/ios/app`：单实例运行管理、重连、停止及状态。
- 只复用 `protocol`、`agent/transport`、`agent/kernel` 等纯 Go 支撑；不导入 `agent/core`、安卓 `agent/commands`、`agent/internal/app` 或安卓 SDK。
- `sdk/ios/runtime` 是结构化原生 JSON 入口，不启动脚本解释器，不反向依赖 Agent。
- Android Agent 继续原有入口；服务端 MCP、租户、权限、Session、操作和调度共用。

## 手机入口

重新编译安装 AgovHost 后，在导航栏打开“Agent 设置”，填写云控 WebSocket 地址与该租户的 Agent 卡密，保存后点击“连接云控”。凭据保存在本机 Keychain，不能填写 FRP token 或调试口令。

默认地址形式为 `wss://你的云控域名/agent`。本地联调可显式允许 `ws://电脑内网IP:端口/agent`，但服务端必须对手机可达，且非回环连接仍须配置云控凭据。电脑服务只监听 127.0.0.1 时手机无法直接连接。

C ABI 装配方法：

```json
{"method":"agent.start","payload":{"server":"wss://你的云控域名/agent","token":"你的云控凭据"}}
```

另有 `agent.status`、`agent.stop`。这些是宿主装配接口，不把 agent.start 或凭据配置开放到公网调试白名单。状态只有收到注册确认后才进入 online；running 表示监督器在运行，不代表设备已在线。

“打开宿主后自动连接”是用户主动启用的配置，不等于系统开机自启动。它在宿主初始化探针结束后执行，避免测试动作抢占云端任务。

从电脑 iosremote 桥迁移时，先停止旧桥，再连接手机原生 Agent，防止同一设备重复注册。首次设备 ID 沿用 vendor 标识摘要，之后保存在手机 Agent 私有目录；目录删除或转移后的身份恢复另行处理。日志和栅栏存储在手机本机，不能复制电脑桥的状态文件冒充迁移完成。

## 当前实现

- 注册平台为 ios，报告系统版本及命令清单；每个连接独立通过 Authorization 请求头鉴权，不修改安卓的全局 transport token。
- 连接代次校验、写入栅栏、动作日志、重复回执与结果查询；正在执行的取消不假装成未执行。
- 同一数据目录跨进程文件锁；停止要等驱动、触点与视频释放；清理失败进入 cleanup_failed，禁止盲目重连或升级。
- 点击、触摸、滑动、长按、中文输入、系统按键、双指缩放和位置轨迹；暂不支持的轨迹压力/接触面积明确拒绝。
- 应用列表/过滤、应用启动及前台确认、应用详情、关闭应用、网址/深链、剪贴板、亮度、亮屏/锁屏、节点树、控件点击/等待、CPU 查询等命令映射。声明有处理器不代表当前系统权限一定放行，必须保留 SDK 的实际错误。
- 视频直接读取 iOS SDK 编码器，使用现有 WS H.264 数据通道，不经过调试 HTTP/FRP，不套用调试接口的 120 秒采集上限；正式长时间采集仍待真机验证。WebRTC 暂不声明。
- SDK 安装与回滚前要求 Agent 已停止且清理确认，不把 iOS arm64 当作 Android arm64 安装。

## 混合设备

服务端根据设备 ID 路由，各平台分别执行。MCP 对 iOS 按实际命令清单预检，Android 既有入口保留；应用名称解析分平台，iOS 不回落 Android 别名。控制台 iOS 不提供 Android 返回键、多任务键、ADB 切换或 ADB 文件管理；混合选择存在不支持按键的设备时禁止该按键群发，不静默丢掉一部分设备。

## 验证和发布

主项目运行 `GOWORK=off CGO_ENABLED=0 go test ./agent/ios/... ./sdk/ios/runtime ./adapters/mcp`。测试覆盖注册、凭据位置、重复动作、跨重连回执、运行中取消、停止等待与编码器释放。

`ios/` 是 Apple 构建镜像。修改主项目后执行 `python ios/tools/sync_agent_sources.py`；使用 `--check` 检查未同步文件。`python ios/tools/check_agent_boundary.py` 验证构建镜像没有传递引入安卓实现。镜像工作流已增加相关触发路径及编译/隔离检查。同步清单按规范化换行校验，避免 Windows CRLF 与 macOS/Linux LF 造成假冲突。

## 尚未完成的验收或能力

本轮需要 GitHub/macOS 编译和真机验证新入口；Android+iOS 同时在线的真实混合测试尚未完成。长时间后台与任务全程常亮不做保证；原生 Agent 的持续投屏、断网重连、升级后恢复需专项测试。

OCR/找图/YOLO、完整文件/存储命令、更多 AX 动作、手机本地脚本下发、iOS SDK 云端升级编排和独立应用列表面板未在本轮补齐。输入插入/替换的跨平台语义也仍需单独统一，不能假定 iOS Unicode 输入天然等同于 Android IME 替换。


本轮已执行：独立运行时/命令/管理器测试、跨平台连接鉴权测试、MCP 能力预检、iOS 应用名称解析、安卓内核包隔离、前端类型及能力判断检查；构建镜像同项测试与依赖边界检查通过。`GOOS=ios GOARCH=arm64 CGO_ENABLED=0 go build ./agent/ios/...` 仅验证 iOS 目标的 Go/stub 交叉编译，不能代替 Apple SDK+cgo 或 ObjC 壳编译。

本地后台已更新并复核：现有电脑桥仍在线；应用列表和前台查询正常；未声明的 app_info 在执行前明确拒绝；iOS 回环连接显示“转发连接”，不再误标 ADB。此回归继续使用已安装的旧手机 SDK，不代表新手机端入口已部署。
