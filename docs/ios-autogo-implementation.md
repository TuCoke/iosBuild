# iOS 构建与历史模拟器记录

> 当前总表：[AutoGo iOS 对标进度与缺项](ios-autogo-status.md)。本文件保留对应分析时点的细节，完成状态以总表为准。

> 后续实现已继续修正：当前输入、视频、AX、应用和识别代码状态见 [本轮对齐记录](ios-autogo-sdk-alignment.md)。本文未修复清单属于该轮核查时点，不能继续当作最新状态。

> 更新：2026-11-11。本文保留此前构建链记录；技术对照与缺项以 [最新核查](ios-autogo-stack-audit.md) 为准。
> 纠正：此前“完全对标”“381 API 缺 0”“71 项全可用”的结论不成立。函数存在和模拟器探针响应不能替代原生功能验证。

## 当前结论

- 我们已有 ObjC 壳、独立 Go SDK 和 Runtime 任务体；这只是结构接近，不是全部 ABI/行为相同。
- 参考样本 libgoeval.dylib 实际为 MH_EXECUTE；不能因后缀将它当作我方 MH_DYLIB 替换件。
- 本轮已修正脚本绑定、任务状态隔离、工程与 embed，以及 AX 快照采集/共享生命周期；新原生代码待 Apple 编译与真机验收。
- Go 视频读取有实现，C 视频回调桥仍有明确缺陷；部分触摸、更新、UI 线程和自检分类问题也待处理，详见最新核查。
- OCR 等识别适配仍引用参考资源预编译库；ImGui 底层版本相同不表示脚本 API 全覆盖。
- 自有入口 agov_ios_invoke/agov_ios_free 不能写成参考实现的“同名同签名入口”。
- Yaegi 为上游 v0.16.3；样本的 app/yaegi 路径不足以证明其修改内容。上游存在 context 取消，已进入的原生调用仍需进程看护。

以下是既有构建记录，未在本轮 Windows 环境重新执行 Apple CI，不自动代表新增原生代码已通过。

## 10. 构建链（GitHub Actions，4 个 job）

| job | runner | 步数 | 作用 |
|---|---|---|---|
| `go-test` | ubuntu | 6 | 单元测试 + precheck（不需要 macOS） |
| `ios-sdk` | macos-15 | 9 | cgo 编译验证（含 `.m`/`.mm`/`cgo CXXFLAGS`） |
| `ios-host` | macos-15 | 21 | 出 dylib + imgui + native 库 + App + ipa/deb |
| `ios-sim` | macos-15 | 16 | 模拟器启动 + 截图 + app.log artifact |

**ios-host 关键步骤**：

```
6  准备 OpenCV framework（只为 cgo 编译头）
7  Go c-archive（不能加 -ldflags "-s"：会剥掉 cgo 导出符号）
8  编 libimgui.dylib（imgui 1.92.5 + cimgui + Metal，断言 ig* 导出）
9  编 libagovnative.a（ag_rec.mm + imgui_overlay.mm）
10 Go 出 dylib（-all_load 两个 .a + -limgui + -lopencv_*）
13 编 Runtime/app（它 import script → 也要链 native 库 + -limgui + rpath）
14 Runtime/*.dylib + libimgui.dylib 进包 + install_name_tool 补 @executable_path/Runtime
16 codesign（ad-hoc + 970 行 entitlements，逐个签）
19 ipa    20 deb
```

### 构建期的 12 个坑（都踩过）

1. `-buildmode=c-shared` 在 ios/arm64 不支持 → c-archive + clang `-dynamiclib`
2. c-archive 加 `-ldflags "-s"` 会剥掉 `agov_ios_invoke` → 符号消失
3. `//export` 注释被代码挤开 → cgo 不生成导出符号（必须紧邻函数）
4. `.mm` 不在 `go/build` 的扩展名表里 → 必须移出 Go 包单独编
5. ObjC++ 调 `.m` 里的 C 函数要 `extern "C"`，否则 `AGJSON(objc_object*)` 找不到 `_AGJSON`
6. `nm … | grep -q` 在 `pipefail` 下会因 SIGPIPE 假失败 → 先落盘再 grep
7. cgo 的 `-I` 用相对路径会失效 → 用 `${SRCDIR}`
8. cgo 不能直接 include C++ 头（cimgui.h）→ 走纯 C 包装层
9. ARС 不允许结构体里放 ObjC 对象 → 面板状态用类
10. `CADisplayLink` 在 QuartzCore，别忘 `-framework QuartzCore`
11. 链接 opencv **framework** 会拖入 Swift 兼容库 → 用它的 `libopencv_*.dylib`
12. PowerShell 生成 workflow 时 `${VAR}` 会被自己展开 → 落盘脚本变形（踩过两次）

## 11. 签名与安装

```
codesign -f -s -  --entitlements host/ios/entitlements.plist  <目标>
```

- **ad-hoc 自签 + 970 行 entitlements**（`platform-application` / `run-unsigned-code` …），**不需要 Apple 账号**
- TrollStore 会读包内 `entitlements.plist` 再装
- `ldid` 解析这份 plist 会崩（`ldid.cpp(852): _assert(): false`），**改用 codesign**
- 越狱机可直接装 `.deb`（不需要签名）
- **不需要本地 Mac**：编译和签名都在 macOS runner 上（Intel/Apple Silicon 都行，产物始终 arm64）

## 12. 已实测的运行时结果（模拟器 app.log）

```
AgovHost 启动（ObjC 壳）
capabilities → ok:true（8 项）
runtime.selftest → touch ok / capture ok（16968 字节）/ injected true
script.run → ok:true，elapsedMs 130，日志 6 条（只跑一次）
sdk: dynamic=true path=…/AgovHost.app/libagovios.dylib
71 项 API 自检 → 可用 44 / 受环境限制 23 / 未实现 0 / 未验证 4
localapi.start → port 18989
```

**模拟器限制**（非 bug）：HID 注入不生效（`计数 0 → 0`）、`app.foreground` 无授权、无 frps 配置。

## 13. 还没对齐的三块

| # | 项 | 他 | 我们 | 影响 |
|---|---|---|---|---|
| 1 | 传输层 | HTTP(gin) + WebSocket + MQTT + QUIC | 仅 `net/http` | 远控方式少 3 种 |
| 2 | 会话层 | `AGAppCtl`（8990，fd + messageID 帧） | 直接 cgo 调 `agov_ios_invoke` | 与他的工具链不互通 |
| 3 | 脚本 UI | `ScriptUI*`（脚本弹原生 UIView） | 只有 hud/console | 脚本做不了自定义原生界面 |

**另**：我们的悬浮层由 ObjC++ 驱动（他是 Go→cimgui 驱动），结果相同、分层不同；
imgui 包也还没注册进 yaegi 符号表（他的脚本能直接 `imgui.Begin(...)`）。

## 14. 下一步

```
P0 真机验证：TrollStore 装 ipa → 验 HID 注入 / 截图 / frpc / 识别栈
P1 传输层补齐（需先确认他的帧格式）
P2 ScriptUI + imgui 注册进 yaegi 符号表
P3 AGAppCtl 会话层（仅在要与他的工具链互通时需要）
```
