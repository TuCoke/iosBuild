import Foundation

// Go c-archive（libagovios.a）的 Swift 封装。
//
// C 侧签名（由 go build -buildmode=c-archive 生成到 libagovios.h）：
//   char *agov_ios_invoke(const char *method, const char *payloadJson);
//   void  agov_ios_free(char *p);
//
// 返回值由 Go 侧 malloc，必须用 agov_ios_free 释放；这里是 JSON 字符串。
enum AgovBridge {
    // 截图走 base64 传回来，这里缓存解码后的二进制供界面显示。
    static var lastImageData: Data?
    static var lastImageWidth = 0
    static var lastImageHeight = 0

    static func invoke(_ method: String, _ payloadJSON: String) -> String {
        lastImageData = nil
        guard let out = method.withCString({ m in
            payloadJSON.withCString { p in
                agov_ios_invoke(UnsafeMutablePointer(mutating: m), UnsafeMutablePointer(mutating: p))
            }
        }) else {
            return "{\"ok\":false,\"error\":\"原生调用返回空\"}"
        }
        defer { agov_ios_free(out) }
        let text = String(cString: out)
        decodeImageIfPresent(text)
        return text
    }

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
