# host/ios/Frameworks

随 App 打包的预编译二进制（不在这里构建）。

## frpc-ios

| 项 | 值 |
| --- | --- |
| 来源 | frp v0.67.0（`cmd/frpc`），取自参考实现包内二进制 |
| 架构 | arm64（`0x0100000C`），MH_EXECUTE |
| 平台 | `LC_BUILD_VERSION platform=iOS`，最低 iOS 13.0 |
| 大小 | 12.8 MB |
| SHA-256 | `7d0003342e19dab1808b6bc24d549c67cbdb2428c40bfc02544aee344299390e` |
| 许可 | Apache-2.0（frp 上游许可） |

存在这个文件时，CI 的「准备 frpc」步骤直接使用它（并打印 `file` / `shasum`），
不再从源码编译（源码编译还要补 `web/frpc/dist` 的 embed 占位，且每次跑要拉几十个模块）。
