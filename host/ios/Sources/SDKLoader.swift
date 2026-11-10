import Foundation

/// SDK 动态加载器（壳方案的核心），用 Swift 实现。
///
/// 【为什么用 Swift 而不是 ObjC】
/// 这段逻辑只是 dlopen + dlsym + 转发，Swift 用 Darwin 模块就能做；
/// 少一个 .m 就少一份 ARC/头文件/编译选项的麻烦（之前正是在那上面卡了两轮）。
///
/// 【三层来源，优先级从高到低】
///   1. 沙盒 <Documents>/Plugins/libagovios.dylib  —— 可写、可下发（热更）
///   2. app 包内 libagovios.dylib                  —— 随包分发（出厂）
///   3. 静态实现（libagovios.a 链进主程序）         —— 失败保险，永远可用
///
/// 【为什么保留静态回退】
/// 动态库加载失败（签名不对、架构不对、权限不够）时 App 不能变砖；
/// 静态那份永远在，只是版本旧一点。
// 直接按符号名声明 Go 的导出函数，不依赖桥接头是否被正确 include。
//
// 为什么这么写：桥接头（Bridging.h → libagovios.h）在 Release 构建里
// 曾出现过 PCH 没刷新、Swift 看不到 C 声明的情况（报 cannot find in scope）。
// @_silgen_name 是编译器级绑定，绕开头文件这一层，行为确定。
@_silgen_name("agov_ios_invoke")
private func agov_ios_invoke(_ method: UnsafePointer<CChar>?, _ payload: UnsafePointer<CChar>?) -> UnsafeMutablePointer<CChar>?

@_silgen_name("agov_ios_free")
private func agov_ios_free(_ p: UnsafeMutablePointer<CChar>?)

final class SDKLoader {
    static let shared = SDKLoader()

    typealias InvokeFn = @convention(c) (UnsafePointer<CChar>?, UnsafePointer<CChar>?) -> UnsafeMutablePointer<CChar>?
    typealias FreeFn = @convention(c) (UnsafeMutablePointer<CChar>?) -> Void

    private var dynamicInvoke: InvokeFn?
    private var dynamicFree: FreeFn?
    private(set) var loadedPath: String = ""
    private(set) var loadError: String = ""
    private var tried = false

    /// 当前是否由动态库在干活。
    var isDynamic: Bool { load() && dynamicInvoke != nil }

    /// 走一次加载探测（幂等）。
    @discardableResult
    func load() -> Bool {
        if tried { return dynamicInvoke != nil }
        tried = true

        var candidates: [String] = []
        if let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first {
            let plugins = docs.appendingPathComponent("Plugins")
            try? FileManager.default.createDirectory(at: plugins, withIntermediateDirectories: true)
            candidates.append(plugins.appendingPathComponent("libagovios.dylib").path)
        }
        let bundle = Bundle.main.bundlePath
        candidates.append((bundle as NSString).appendingPathComponent("libagovios.dylib"))
        candidates.append((bundle as NSString).appendingPathComponent("Frameworks/libagovios.dylib"))

        for path in candidates {
            guard FileManager.default.fileExists(atPath: path) else { continue }
            guard let handle = dlopen(path, RTLD_LAZY) else {
                if let reason = dlerror() {
                    loadError = "dlopen 失败: \(String(cString: reason))"
                } else {
                    loadError = "dlopen 失败: \(path)"
                }
                continue
            }
            guard let invokeSym = dlsym(handle, "agov_ios_invoke"),
                  let freeSym = dlsym(handle, "agov_ios_free") else {
                loadError = "dylib 缺少入口 agov_ios_invoke/agov_ios_free: \(path)"
                dlclose(handle)
                continue
            }
            dynamicInvoke = unsafeBitCast(invokeSym, to: InvokeFn.self)
            dynamicFree = unsafeBitCast(freeSym, to: FreeFn.self)
            loadedPath = path
            loadError = ""
            return true
        }
        return false
    }

    /// 尝试重新加载。返回 true 表示已切到新库；
    /// 返回 false 通常意味着"已经加载过 dylib，需要重启进程"（iOS 不能干净卸载已加载的库）。
    @discardableResult
    func reload() -> Bool {
        if dynamicInvoke != nil { return false }
        tried = false
        return load()
    }

    /// 统一入口：动态库在就用动态库，否则回退到静态实现。
    func invoke(_ method: UnsafePointer<CChar>?, _ payload: UnsafePointer<CChar>?) -> UnsafeMutablePointer<CChar>? {
        load()
        if let fn = dynamicInvoke { return fn(method, payload) }
        return agov_ios_invoke(method, payload)
    }

    /// 释放 SDK 返回的字符串（动态/静态各用各的 free）。
    func freeString(_ p: UnsafeMutablePointer<CChar>?) {
        load()
        if let fn = dynamicFree { fn(p); return }
        agov_ios_free(p)
    }
}
