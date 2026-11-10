# host/ios/Frameworks

存放随 App 一起打包的预编译二进制。目前只有 `frpc-ios`。

## frpc-ios

| 项 | 值 |
| --- | --- |
| 来源 | frp v0.67.0（`cmd/frpc`），从参考实现的包内二进制取来 |
| 版本证据 | 二进制内嵌 `github.com/fatedier/frp@v0.67.0/...` 源码路径 |
| 架构 | arm64（`0x0100000C`），MH_EXECUTE |
| 平台 | `LC_BUILD_VERSION platform=iOS`，最低 iOS 13.0 |
| 大小 | 12.8 MB |
| SHA-256 | `7d0003342e19dab1808b6bc24d549c67cbdb2428c40bfc02544aee344299390e` |
| 许可 | Apache-2.0（frp 上游许可，二进制品随包分发需保留此声明） |

## 为什么用预置二进制而不是每次 CI 现编

```
① GOOS=ios 强制要 cgo → 只能在 macOS + iPhoneOS SDK 下编，本机 Windows 编不了
② 每跑一次 CI 都要 clone frp + 拉几十个模块，慢且依赖外网
③ 二进制版本固定，产物可复现（sha256 就写在上面的表里）
```

CI 里的「准备 frpc」步骤会优先用这里的文件；文件缺失或异常小的时候才回退到
从源码编译（同一个 v0.67.0）。「断言 frpc 已进包」步骤会检查它真的被复制进
`AgovHost.app/`，缺失直接失败 —— 避免"以为打进去了，其实没有"。

## 换版本时怎么做

1. 替换 `frpc-ios`，更新上面的版本号与 sha256
2. 确认 `file frpc-ios` 输出是 `Mach-O 64-bit executable arm64`
3. 确认 `LC_BUILD_VERSION` 的 platform 是 `iOS`（不是 `macOS`，也不能是
   `iOSSimulator`）—— 平台不对的表现是设备上 `posix_spawn` 直接 EACCES
4. 跑一次 CI，看「准备 frpc」与「断言 frpc 已进包」两步的输出
