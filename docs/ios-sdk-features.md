# iOS SDK 新增功能与使用约定

更新时间：2026-11-11。源码已完成本地纯 Go 编译与静态检查，GitHub 四项构建和既有模拟器回归已通过；新增功能的完整行为仍待验收，详见 [检查记录](ios-sdk-ci-20261111.md)。

主源码在 `v2/sdk/ios/`，Apple 构建镜像在 `v2/ios/`。本轮增加能力接口及宿主装配，没有修改 Agent/MCP 协议或设备调度框架。

## OpenCV 图像与模板匹配

原生算法仍使用 OpenCV 4.10，与已提供的 `libopencv_core.dylib` 版本字符串一致。Go 层持有原生注册表的 Mat 令牌，不暴露 cv::Mat 指针；原生注册表统一管理对象，每个派生图像拥有独立像素。当前支持：

- `Decode`、`FromImage`、`Info`、`Clone`、`Close`。
- `Crop`、`Resize`、`Rotate`、`Gray`、`Threshold`、`GaussianBlur`、`Canny`、PNG/JPEG `Encode`。
- `FindImage`、`FindImageAll`，支持灰度、透明/左上角色键掩码、阈值、结果上限及重叠抑制。
- `Scope` 管理一次任务及所有派生 Mat；解释器自动给构造器绑定任务 Scope，结束、失败或取消后回收。

图像上限为 8192×8192 且不超过 8M 像素；进程最多 128 个 Mat、128 MiB 保留像素。未交付原生库的环境返回错误，不返回假的空 Mat。模拟器目前没有对应 OpenCV 运行库，因此此边界明确不支持模拟器，不能把旧的模拟器截图验证当成新增 OpenCV 验证。

```go
ctx := context.Background()
scope, err := opencv.NewScope(ctx)
if err != nil { return err }
defer scope.Close()
frame, err := capture.Screenshot(ctx, capture.Options{AutoRotation: true})
if err != nil { return err }
screen, err := scope.Decode(ctx, frame.Data)
if err != nil { return err }
templateBytes, err := os.ReadFile("button.png")
if err != nil { return err }
template, err := scope.Decode(ctx, templateBytes)
if err != nil { return err }
match, found, err := opencv.FindImage(ctx, screen, template,
    opencv.MatchOptions{Gray: true, Threshold: 0.95})
// found=false 是未找到；err!=nil 是环境或参数失败，两者不能混淆。
_ = match; _ = found
return err
```

匹配分数为 `1 - 平均像素平方差 / 255²`，完全相同为 1。它不是对样本未知阈值算法的等价承诺。当前接口也没有声称覆盖全部 OpenCV 类型和函数。

## ImGui 脚本界面

`Run(ctx, draw)` 将绘制函数放到宿主主线程 Metal 帧内，一次只允许一个回调。任务结束会解绑；panic 返回错误并恢复 ImGui 栈。脚本不调用 `NewFrame`，由原生循环统一管理帧。

新增 44 项已核对 C 签名的生成接口，以及 `Checkbox`、`SliderFloat`、`SliderInt`、`InputText` 的可变缓冲包装。包含按钮、布局、分组、树、表格、弹窗、提示、禁用状态和交互查询。生成器位于 `v2/ios/tools/imgui_bindings.py`，不会把未知结构体和回调猜成可调用 ABI。

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
enabled := true
return imgui.Run(ctx, func() {
    imgui.Begin("脚本设置")
    defer imgui.End() // Begin 返回 false 时也必须 End。
    imgui.Checkbox("记录执行结果", &enabled)
    if imgui.Button("结束", 120, 36) { cancel() }
})
```

触摸通过 UIWindow 的命中区域与 UIKit 事件进入 ImGui。原生 UITextField 作为输入代理，中文组词完成后才提交给 ImGui，退格单独转换为按键事件；这些新增行为尚未实测。该窗口仍属于自身 WindowScene，**不是跨 App 全局悬浮宿主**；独立脚本进程没有场景时明确报不支持。

## JavaScriptCore 原生 UI 桥

宿主新增 `AGJSBridge`、`AGJSSelector`、`AGJSStruct`、`AGJSActionTarget`，按 JSContext 隔离对象、控件和事件。开放有类型的能力，不使用任意 KVC 或未校验的 selector 调用：

- `Rect/Point/Size`、`ui.create/get/clear`、控件 `get/set/on/remove`。
- view、label、button、input、switch、slider、text、scroll、stack。
- 帧、文字、背景、显隐、启用、placeholder、值、父子关系及 click/change 回调。
- `sdk.call(method, args)` 调用设备能力；任务启动和升级由 Go 脚本/宿主管理，防止在同一 JS 队列内递归等待自己。
- `sendToScriptAsync(message)` 使用 8990 协议的状态 6 请求及状态 0/3 应答，超时 15 秒；原有 `sendToScript` 保留单向消息。

通过 `apkctl.Eval` 提交的 JS 示例：

```javascript
const run = ui.create("button", "run", {
  frame: Rect(24, 100, 220, 48),
  text: "提交结果", background: "#2277EE"
});
run.on("click", event => {
  sendToScriptAsync(JSON.stringify(event))
    .then(() => run.set({text: "已接收"}))
    .catch(error => run.set({text: String(error)}));
});
```

Go 端用本次任务的 `apkctl.SetCallback` 接收。应答只表示 Go 回调正常返回，不等于业务目标已完成；不要把这个 ACK 当作截图、发布或支付成功证明。

每个连接最多 32 个上下文、32 个等待 Eval、128 个待应答消息；每个上下文最多 256 个控件。UI 事件异步送到所属 JS 队列，回调有时间上限。断连清理对象、回调和窗口。这里仍是自身 App 的原生 UI 桥，不保证任意 AutoGo 私有类/方法和协议可互换。

## 原生依赖交付

锁文件：`v2/ios/host/ios/native-resources.lock.json`；工具：`v2/ios/tools/native_resources.py`。

锁定已有 iosBuild 提交 `54cfbb5e4c623411f9e79de9ef7c1911c03dc9a4` 中的 7 个库的大小、SHA256、平台、包内路径和来源。PPOCR 验证仓文件与 resources 原始文件的摘要不同，当前明确使用验证仓版本，不能混装。原生运行库的本地与 CI 静态交付校验均已通过。

在独立 Apple 构建目录内：

```sh
python3 tools/native_resources.py --fetch --groups opencv,recognition
# 离线来源也必须符合相同摘要：
python3 tools/native_resources.py --fetch --source-dir /path/to/locked-libs
```

资源已在验证仓/CI 中完成静态校验；这些命令不执行识别引擎。发布工作流已接入缺件即失败，避免静默省略 OCR 库；构建时会把锁文件放进 App。运行期 `resources.Inspect(ctx, false)` / `resources.status` 检查实际文件和平台，`verifyHash=true` 进一步比较原始摘要。

签名和 install_name_tool 会改变 Mach-O 摘要，运行期会如实报告差异；发布前校验不能用打包后的不同摘要冒充通过。`Ready` 只表示所检查资源的交付检查通过，不表示引擎符号可用或识别效果合格。模型仍由调用方明确提供，尚没有模型版本与签名发布闭环；预编译引擎也不是本仓库已经自建的源码。

## SDK 更新和回退

新的宿主启动器与 SDK 必须配套安装一次：旧宿主不认识新的版本目录和状态文件，不能只替换 Go dylib 就宣称 ObjC 宿主已升级。

1. 检查 Mach-O/arm64/dylib、公开入口、SHA256 和 `LC_BUILD_VERSION` 平台，拒绝真机/模拟器混装。
2. 安装到 `Documents/Plugins/versions/<sha256>/libagovios.dylib`，不覆盖正在使用的文件。
3. 原子保存 `release.json` 的 pending，等待重启；运行中的脚本继续使用实际加载的同一文件。
4. 宿主加载前重新核对摘要，先记录 attempt 再 dlopen。
5. 启动 10 秒后调用 `sdk.health`，响应通过后才设为 current，并保留 previous。
6. 确认前退出、dlopen 失败、摘要不符或健康检查失败，下次启动自动选择已确认版本/包内版本。`sdk.rollback` 可安排回退或取消尚未启动的候选。

健康确认只检查运行时协议响应；权限受限的 AX、截图不能误判为升级失败。运行时原生调用无法被线程安全强行中断，若检查本身阻塞，attempt 会保留，下次启动回退；不是在线卸载当前 Go runtime，也不承诺无需重启。长期设备健康、依赖 ABI/模型兼容清单、版本清理、签名发布信任链及真实升级验收仍待补齐。

## AX 预热

新增 `uiacc.Warmup(ctx, 2500*time.Millisecond, true)`：激活环境、主线程中心查询、50ms 间隔有界重试，最后恢复首次读取的设置，保留预热状态，避免下次快照立即重新开启设置。

它不在并发 Go 进程内切换 uid/gid；恢复的是用户原有值，而不是无条件关闭。默认 Bootstrap 的既有行为保留，此轮增加的是显式预热接口，没有用未验证的新默认路径覆盖原先已经通过的截图/AX 链路。

剩余范围见 [AutoGo 对标总表](ios-autogo-status.md)。源码完成与实际效果确认必须分开记录。
