# iOS SDK 构建与宿主

主能力源码在 `v2/sdk/ios/`；本目录 `v2/ios/` 提供 Apple 构建镜像和原生宿主。GitHub 独立验证仓的相同布局直接位于仓库根目录。

本轮本地静态检查、纯 Go 编译和 GitHub 四项检查已通过。新增功能的完整行为与真机验收仍待补齐：

- [能力对标及剩余缺项](docs/ios-autogo-status.md)
- [新增功能、调用示例及更新流程](docs/ios-sdk-features.md)
- [本地编译与 GitHub 验证记录](docs/ios-sdk-ci-20261111.md)

## 已验证的代码版本

- GitHub 仓库：[TuCoke/iosBuild](https://github.com/TuCoke/iosBuild)。
- 本轮验证分支：[codex/ios-sdk-features-20261111](https://github.com/TuCoke/iosBuild/tree/codex/ios-sdk-features-20261111)。不表示已经合并至 GitHub main。
- 通过验证的代码提交：`3c08dbec75a940d99ef2a7db7cee68ef1088545d`。
- [GitHub Actions：4/4 通过及产物](https://github.com/TuCoke/iosBuild/actions/runs/43001526920)。包含 Go 检查/测试、iPhoneOS/CGo 编译、原生宿主/动态库/IPA/DEB 打包、模拟器基础回归。
- 模拟器验证了 SDK 截图、79/80 个 AX 节点、独立脚本进程和 JS 基础通道；没有完成实体 iPhone、新增全部控件、找图算法和升级回退的完整行为验收。

## 目录与职责

```text
sdk/ios/                      主能力源码的构建镜像
sdk/imaging/                  公共图像计算依赖
cmd/
├── ioshost/                  Go 动态 SDK 的 C ABI 入口
├── runtimeapp/               Go 文件解释入口；原生 Runtime/app 调用相同 SDK
├── symbolgen/                Yaegi 能力符号表生成
└── precheck/                 Go/ObjC/ObjC++、分派、C 链接与构建约定检查
host/ios/
├── Sources/                  ObjC 壳、SDKLoader、JS 原生 UI、控制服务与检查界面
├── objc/                     ag_rec、ag_cv、imgui_overlay 原生胶水
├── third_party/cimgui/       cimgui、ImGui、Metal 后端及自维护包装层
├── RuntimeMain.m             独立任务薄执行体、父进程看护与超时
├── Runtime/                  构建时获取的识别/OpenCV 运行库
├── Frameworks/               构建产物与 frpc
├── native-resources.lock.json 原生库来源、摘要、大小、平台与包内路径
├── project.yml               xcodegen 宿主工程；壳通过 dlopen 加载 Go SDK
└── entitlements.plist         已授权运行环境所需的能力声明
tools/
├── imgui_bindings.py          已核对 ABI 的控件绑定生成器
└── native_resources.py        原生资源获取与静态校验
docs/                        对标、功能、编译与历史诊断记录
.github/workflows/
├── ios-sdk.yml               Go / iOS SDK / 宿主打包 / 模拟器四项检查
└── reverse.yml               参考样本静态分析工作流
```

`Runtime/app` 是原生薄执行体，加载宿主实际使用的同一个 `libagovios.dylib`，不另装一套 Go SDK。UIKit 主线程必须保持事件循环。

## 原生资源交付

主仓库不提交分析样本和预编译大文件。构建需要的 7 个识别/OpenCV 库通过 `host/ios/native-resources.lock.json` 锁定，发布前缺件、摘要或平台不符即失败。

锁定的是已有验证仓提交中的产物；算法引擎并非本仓库已实现的自建源码。PPOCR 验证仓文件与 resources 原始样本摘要不同，按清单选用，不能混装。模型仍由调用方明确提供。frpc 可以使用匹配的预编译产物或工作流中的源码构建回退。

从本构建目录执行（主仓库为 `v2/ios/`，独立验证仓为仓库根目录）：

```sh
python3 tools/native_resources.py --fetch --groups opencv,recognition
# 受信任的离线目录也必须符合锁定摘要：
python3 tools/native_resources.py --fetch --source-dir /path/to/locked-libs
```

## 本地静态检查与编译

Windows 可以检查源码、纯 Go 构建及资源元数据；ObjC/ObjC++/CGo 的 iOS 编译需要 Apple SDK/Xcode。以下命令从本构建目录执行：

```sh
go run ./cmd/precheck
# POSIX shell 写法；PowerShell 用 $env:CGO_ENABLED='0' 设置环境变量。
CGO_ENABLED=0 go build ./sdk/... ./cmd/runtimeapp ./cmd/symbolgen ./cmd/precheck
CGO_ENABLED=0 go vet ./sdk/... ./cmd/runtimeapp ./cmd/symbolgen ./cmd/precheck
python3 tools/native_resources.py --groups opencv,recognition
```

`precheck` 是源码/约定检查，不替代 Apple 编译器或运行时验收。GitHub 的 `ios-sdk.yml` 使用 macOS runner 完成原生构建和模拟器检查，产出 IPA/DEB、编译对象、日志和截图。包可用于后续匹配系统版本与授权环境的安装验收，打包成功不等于所有设备功能已验证。
