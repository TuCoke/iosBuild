# iOS 真机自检与原生读取修正

## 问题与修改

| 截图现象 | 确认的问题 | 本轮修改 |
| --- | --- | --- |
| system.memory / system.cpu 失败 | 自检固定访问 PID 1，权限不足被误解为基础读取能力失效 | 基础自检使用宿主真实 PID，报告保存实际 args；其他 PID 的权限错误保留 |
| app.icon 失败 | 自检使用虚构 bundleId `__probe__`；原生实现仅尝试 proxy.iconForVariant | 改用 app.self 的真实包名；兼容系统 UIImage 图标入口、代理入口与真实包内资源，不生成占位图冒充成功 |
| app.foreground 为空 | 单个 SpringBoardServices 入口返回空值即失败 | 主线程查询 SpringBoard；仅当前进程确有前台活动 Scene 时返回自身；否则从当前屏幕 AX 应用对象/快照读真实包名，并标明 source |
| device.wake 不支持 | 将亮屏错误地绑定到 SBSUnlockDevice 符号 | 改为先读取亮屏状态；已亮屏不按电源键，熄屏时提交 HID Consumer 电源键并检查状态。不尝试解除密码锁 |

## 进程读取

参考样本 BridgeGetMemoryUsageKB / BridgeGetCpuUsage 调用 `proc_pidinfo`，flavor 4，96 字节任务结构。本实现按相同 Darwin ABI 提供读取，并校验完整返回大小，绕开 iOS SDK 缺少 proc_info.h 的编译问题。

- 内存：proc_pidinfo → proc_pid_rusage → 仅自身可用的 task_info。
- CPU：proc_pidinfo → proc_pid_rusage → 仅自身可用的 getrusage。
- 不得把自身数据作为其他 PID 的降级结果。PID 0 表示自身，负数拒绝。
- CPU 分母使用真实单调时钟采样窗口，不固定假定 usleep 一定耗时 200ms。保留单核心 100% 的统计口径，多线程进程可超过 100%。
- 错误保留目标 PID、读取来源或 errno，无法读取时返回真实失败。

ABI 与时间口径核对：[Darwin proc_info](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/proc_info.h)、[iOS 16 同期 XNU bsd_kern](https://github.com/apple-oss-distributions/xnu/blob/xnu-8792.61.2/osfmk/kern/bsd_kern.c)。本项目代码不包含第三方闭源二进制反编译源码拷贝。

## 自检与真实效果的边界

- 全量自检仍不是跨应用、跨权限的全功能验收。基础读取测真实宿主，受限目标仍可能报错。
- 无法取得 app.self 时，应用读取项记为 unverified，不使用假包名凑成功。
- device.wake 有副作用，自检记为 unsafe 跳过；不能用自检“没有 unsupported”宣称真机亮屏已验证。
- AX 后备结果来自实际屏幕应用，带 source；系统弹窗、键盘等覆盖场景仍需真机核对。没有结果时保留失败，不把缓存或宿主包名当答案。
- 图标读取失败仍返回真实错误，不能保证所有系统/第三方 App 在所有权限环境均可读。

本轮仅修改 SDK 原生读取、亮屏及自检，不修改调试服务、frp 配置、监听方式或保活策略，也未连接/操作用户正在调试的手机。

本地静态检查通过；GitHub 增加实际宿主 PID、真实图标包名及亮屏自检跳过规则的运行回归。原生编译和真机效果需分别记录，不沿用此前构建结果。
