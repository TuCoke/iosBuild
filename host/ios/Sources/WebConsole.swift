import SwiftUI
import WebKit

/// 设备端 Web 控制台（WKWebView）。
///
/// 【为什么要它】SwiftUI 界面是编译进壳的，改一次要重装；
/// 而 Web 页面放在沙盒 UI/ 目录里，服务端下发新的 index.html 就能换界面 ——
/// 这就是"UI 热更"。壳只提供容器和本地 HTTP 服务（127.0.0.1:18989/ui/）。
///
/// 【安全边界】只加载 127.0.0.1，不加载外网页面：
/// 页面能调 /invoke 打到全部 SDK 能力，等于设备控制权。
struct WebConsoleView: UIViewRepresentable {
    let url: URL

    func makeUIView(context: Context) -> WKWebView {
        let config = WKWebViewConfiguration()
        config.allowsInlineMediaPlayback = true
        let webView = WKWebView(frame: .zero, configuration: config)
        webView.isOpaque = false
        webView.backgroundColor = .clear
        webView.load(URLRequest(url: url))
        return webView
    }

    func updateUIView(_ webView: WKWebView, context: Context) {
        // 地址没变就不重复加载，避免每次刷新界面都把页面顶回首页
        if webView.url?.absoluteString != url.absoluteString {
            webView.load(URLRequest(url: url))
        }
    }
}

/// 全屏呈现的 Web 控制台。
///
/// 关闭用回调而不是 @Environment(\.dismiss)：后者是 iOS 15+ API，
/// 我们的最低版本是 14.0，用了会在编译期报 availability 错误。
struct WebConsoleScreen: View {
    let url: URL
    let onClose: () -> Void

    var body: some View {
        NavigationView {
            WebConsoleView(url: url)
                .navigationBarTitle("控制台", displayMode: .inline)
                .navigationBarItems(trailing: Button("关闭") { onClose() })
        }
        .navigationViewStyle(StackNavigationViewStyle())
    }
}
