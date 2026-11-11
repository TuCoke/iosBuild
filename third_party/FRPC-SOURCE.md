# frpc 源码交付

- 上游：https://github.com/fatedier/frp
- 版本：v0.67.0，Apache-2.0，许可证保留在 frp/LICENSE。
- 标签对象：63a21198138e2ca57031eb8c9c2c4f7255586554。
- 实际提交：5f575b84420a19350467290c5c2bf9f4c7b27e57。
- frp/ 为该提交的完整 Git archive 源码，不包含嵌套 .git 或第三方预编译客户端。
- frp-source.lock.json 记录所有原始文件的 SHA256；修改上游代码时须显式审核和更新锁定清单。

构建先执行 `python3 tools/verify_frpc_source.py`，不再从 GitHub 临时克隆 frp。
源码使用独立 go.mod，设置 GOWORK=off，依赖使用 -mod=readonly 与上游 go.sum；
Go 模块依赖首次仍需下载，Actions setup-go 缓存额外包含 third_party/frp/go.sum。
每次发布仍编译 iOS 产物，不能用本机 Windows 编译成功代替 iOS 验证。

Web 管理页面不启用，只在构建时生成 web/frpc/dist/index.html 满足 go:embed。
这不会替换网络协议或命令行；iOS 客户端保持官方 `frpc -c 配置文件` 接口。
