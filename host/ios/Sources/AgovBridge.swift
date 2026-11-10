import Foundation

// SDK 的 Swift 封装。
//
// 走 SDKLoader：优先加载沙盒/包内的 libagovios.dylib，
// 没有才回退到链进主程序的静态实现 —— 这就是"换 SDK 不重装"的入口。
//
// 返回值由 SDK 侧 malloc，必须用 SDKLoader.freeString 释放；这里是 JSON 字符串。
enum AgovBridge {
    // 截图走 base64 传回来，这里缓存解码后的二进制供界面显示。
    static var lastImageData: Data?
    static var lastImageWidth = 0
    static var lastImageHeight = 0

    static func invoke(_ method: String, _ payloadJSON: String) -> String {
        lastImageData = nil
        guard let out = method.withCString({ m in
            payloadJSON.withCString { p in
                SDKLoader.shared.invoke(UnsafeMutablePointer(mutating: m), UnsafeMutablePointer(mutating: p))
            }
        }) else {
            return "{\"ok\":false,\"error\":\"原生调用返回空\"}"
        }
        defer { SDKLoader.shared.freeString(out) }
        let text = String(cString: out)
        decodeImageIfPresent(text)
        return text
    }

    /// 当前 SDK 是不是动态库在干活（false = 用的是编进主程序的静态实现）。
    static var isDynamic: Bool { SDKLoader.shared.isDynamic }

    /// 当前加载的 dylib 路径（静态实现返回空串）。
    static var loadedPath: String { SDKLoader.shared.loadedPath }

    /// 最近一次动态加载失败的原因（成功时为空）。
    static var loadError: String { SDKLoader.shared.loadError }

    /// 尝试重新加载动态库。返回 true 表示已切换到新库；
    /// 返回 false 通常意味着"已经加载过 dylib，需要重启进程"（iOS 不支持卸载已加载的库）。
    static func reloadSDK() -> Bool { SDKLoader.shared.reload() }

    private static func decodeImageIfPresent(_ json: String) {
        guard let data = json.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let payload = obj["data"] as? [String: Any],
              let b64 = payload["base64"] as? String,
              let decoded = Data(base64Encoded: b64) else { return }
        lastImageData = decoded
        lastImageWidth = payload["width"] as? Int ?? 0
        lastImageHeight = payload["height"] as? Int ?? 0
    }
}
