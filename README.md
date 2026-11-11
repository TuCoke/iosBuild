# iosBuild — iOS 设备能力 SDK（编译验证仓）

当前完整对标与缺项：[iOS SDK 状态总表](docs/ios-autogo-status.md)。

这个仓库只放 **iOS 能力 SDK 的源码** 和 **它的编译验证工作流**，不含服务端、控制台、安卓任何内容。

最新截图/AX 根因、修复和 SDK 原生 JPEG 验收记录：[截图与节点树失败定位](docs/ios-capture-ax-diagnostics.md)。模拟器结果与真机验收边界分别记录。

- 主仓库（Gitee）：`agent-cgo` 的 `v2/sdk/ios/`
- 本仓库作用：让 GitHub 的 macOS 跑器用**真正的 iPhoneOS SDK** 编译一遍，验证 `go build`（cgo）与原生 Objective-C/ObjC++ 文件都能过

## 这个仓库里有什么

```
go.mod                      module autogov1/v2（依赖见 go.mod）
go.sum                      Go 依赖校验值
sdk/ios/
├── app/ capture/ device/ ime/ motion/ uiacc/ video/ types/    能力包
├── internal/inputstate/                                       进程内输入串行闸门
├── internal/native/                                           原生 .m + bridge.h + bridge_ios.go
└── examples/                                                  调用示例
.github/workflows/ios-sdk.yml                                  编译验证（只用 macOS 跑器）
```

## 编译验证做了什么

每次 push 到 `sdk/ios/**` 会触发 `ios-sdk` 工作流（macOS 15 / Apple Silicon / 自带 Xcode）：

1. 环境自检：`sw_vers`、`xcodebuild -version`、`go version`、iPhoneOS SDK 路径
2. **用真实 iPhoneOS SDK 编译全部 `.m`**
   - 必须带 `-fno-objc-arc`：代码是 MRR（手动引用计数），开 ARC 会报 `release`/`dealloc` 错误
   - 不 import `IOSurface/IOSurface.h`：iOS SDK 里没有这个头（它是 macOS 的），代码改用 `dlsym`
3. `go build ./...` + `go test ./...`（`CGO_ENABLED=0`，走非 iOS stub）
4. **`GOOS=ios GOARCH=arm64 CGO_ENABLED=1 go build ./...`** —— 验证 Go + cgo + ObjC 桥能一起编过
5. 上传 `.o` 产物（保留 7 天）

## 它证明了什么、没证明什么

**已证明（真机 SDK 编译）：**
- 6 个 `.m` 能被 iPhoneOS SDK 编译（2026-11-09 在 macOS 15.7.7 / Xcode 26.3 / SDK 26.2 上实测 6/6 通过）
- Go 侧代码与非 cgo 路径的单测通过

**未证明：**
- 链接、签名、安装到真机
- 运行行为：私有 API（IOHIDEvent 注入、CARenderServer 截图）在各机型/iOS 版本上的实际效果
- 控件树采集：`uiacc` 只有查询与采集器契约，没有原生 AX 采集实现

## 私有 API 的处理原则

iOS 上要操作其它 App，只能用私有 API。这里的做法是：

- **不 import 私有头文件**（`IOSurface.h` 之类在 iOS SDK 里根本不存在）
- 本地声明类型，函数一律 `dlsym`（`AGSymbol`），链接期不依赖私有框架
- 解析不到就返回明确错误，**不假装成功**

用到的私有入口：`IOHIDEventSystemClientCreate` / `IOHIDEventCreateDigitizerFingerEvent` / `IOHIDEventSystemClientDispatchEvent` / `CARenderServerRenderDisplay` / `IOSurface*`。

## 本地怎么跑（macOS，可选）

```bash
./build-on-mac.sh 2>&1 | tee build.log     # 如果从主仓库带过来的话
```

或者直接：

```bash
SDK=$(xcrun --sdk iphoneos --show-sdk-path)
for f in sdk/ios/internal/native/*.m; do
  xcrun --sdk iphoneos clang -c -arch arm64 -mios-version-min=14.0 \
    -isysroot "$SDK" -fno-objc-arc -fblocks -o /tmp/x.o "$f"
done

CGO_ENABLED=1 GOOS=ios GOARCH=arm64 \
CC="$(xcrun --sdk iphoneos -f clang)" \
CGO_CFLAGS="-arch arm64 -mios-version-min=14.0 -isysroot $SDK -fno-objc-arc" \
go build ./...
```

## 2026-11-11 SDK 对齐验证

本轮同步输入、方向、Unicode、截图/视频回调、AX、识别 ABI、脚本生命周期与图像算法。变更依据和仍未验证的能力见 [技术对齐记录](docs/ios-autogo-sdk-alignment.md)。本分支用于 GitHub Actions 原生编译验证；构建通过也不代表跨 App 能力已通过真机验收。

新增公开图像处理依赖的许可文本见 `third_party/licenses`。
