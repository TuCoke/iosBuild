# reverse/

这里放**逆向分析用的二进制**，不参与构建、不进 app 包。

| 文件 | 大小 | 来源 |
| --- | --- | --- |
| libgoeval.dylib | 39.7 MB | 参考实现的 iOS SDK 动态库（yaegi + 全部能力） |
| AutoGoRunner | 0.5 MB | 参考实现的壳（ObjC，无 Go 代码） |

用途：`.github/workflows/reverse.yml`（手动触发）会用 macOS 的 `nm` / `otool`
导出符号表与关键函数的反汇编，产出一份文本报告，供我们确定
`GoMain` / `AXBootstrap` / `AXSweepNodesJSON` / `AGAppCtl*` 的真实签名。

注意：这两个文件**只用于分析**，不随我们的 App 分发。
