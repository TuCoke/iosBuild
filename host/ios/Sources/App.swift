import SwiftUI
import UIKit

// 最小诊断 App：验证 SDK 的私有 API 在这台设备上到底能不能用。
//
// 三个自证式测试（不依赖其它 App）：
//   1) 能力探测：IOHIDEvent / CARenderServer 等符号能不能解析到
//   2) 注入点击：往屏幕中心注入一次点击，中间红色方块计数 +1 即说明注入生效
//   3) 注入上滑：往列表注入一次上滑，列表真的滚动即说明滑动注入生效
//   4) 截图：走 CARenderServer + IOSurface，能把图显示出来即说明采集可用

@main
struct AgovHostApp: App {
    var body: some Scene {
        WindowGroup {
            ContentView()
        }
    }
}

struct LogLine: Identifiable {
    let id = UUID()
    let text: String
}

struct ContentView: View {
    @State private var log: [LogLine] = []
    @State private var tapCount = 0
    @State private var image: UIImage? = nil
    @State private var imageInfo = ""

    private let rows = (1...40).map { "列表行 \($0) — 用来验证滑动注入" }

    var body: some View {
        VStack(spacing: 8) {
            ScrollView {
                LazyVStack(spacing: 6) {
                    ForEach(rows, id: \.self) { row in
                        Text(row)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.vertical, 6)
                            .padding(.horizontal, 10)
                            .background(Color.gray.opacity(0.15))
                            .cornerRadius(6)
                    }
                }
                .padding(.horizontal, 12)
            }
            .frame(height: 150)

            // 中央目标：被注入点击命中时计数 +1
            Text("被注入点击：\(tapCount)")
                .font(.title3).bold()
                .foregroundColor(.white)
                .frame(maxWidth: .infinity)
                .frame(height: 84)
                .background(Color.red)
                .cornerRadius(10)
                .padding(.horizontal, 12)

            HStack(spacing: 8) {
                Button("能力探测") { callCapabilities() }
                Button("自检") { callSelfTest() }
                Button("设备信息") { callDeviceInfo() }
                Button("截图") { callScreenshot() }
            }
            .buttonStyle(BorderlessButtonStyle())
            .padding(.horizontal, 12)

            HStack(spacing: 8) {
                Button("Web 控制台") { showWebConsole = true }
                Button("启动穿透") { startTunnel() }
                Button("停止穿透") { stopTunnel() }
                Button("隧道状态") { tunnelStatus() }
            }
            .buttonStyle(BorderlessButtonStyle())
            .padding(.horizontal, 12)

            HStack(spacing: 8) {
                Button("跑脚本") { runScript() }
                Button("注入点击(中心)") { injectTap() }
                Button("注入上滑") { injectSwipe() }
                Button("清空日志") { log.removeAll() }
            }
            .buttonStyle(BorderlessButtonStyle())
            .padding(.horizontal, 12)

            if let image = image {
                Image(uiImage: image)
                    .resizable()
                    .scaledToFit()
                    .frame(maxHeight: 160)
                    .overlay(
                        Text(imageInfo)
                            .font(.caption)
                            .padding(4),
                        alignment: .bottomLeading
                    )
            }

            ScrollView {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(log) { line in
                        Text(line.text)
                            .font(.system(size: 11, design: .monospaced))
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
                .padding(.horizontal, 12)
            }
        }
        .padding(.vertical, 8)
        .sheet(isPresented: $showWebConsole) {
            WebConsoleScreen(url: consoleURL) { showWebConsole = false }
        }
        .onAppear {
            // SwiftUI 会多次触发 onAppear（界面重算就会再来一次），
            // 不加这道闸，脚本会跑好几遍、日志也会重复。
            guard !didStartup else { return }
            didStartup = true
            resetLogFile()
            append("AgovHost 启动")
            callCapabilities()
            // 启动时顺手跑一次自检：这样 CI 截一张图就能看到
            // "真投一次点击 / 真抓一帧"的实际结果，不用人去点按钮。
            callSelfTest()
            // 启动时也跑一次内置脚本：CI 截图即可验证 yaegi 解释器与热更新链路。
            runScript()
            // 报告 SDK 来源：静态（编进主程序）还是动态 dylib（可热更）。
            // 这两行是"壳方案是否生效"的唯一判据，一眼就能看到。
            append("sdk: dynamic=\(AgovBridge.isDynamic) path=\(AgovBridge.loadedPath)")
            if !AgovBridge.loadError.isEmpty { append("sdk loadError=\(AgovBridge.loadError)") }
            // 逐个按钮探测：把每个按钮背后的 op 各调一次，结果写进日志。
            // 这样一张截图就能看出"哪些按钮真的可用"，不用人手点。
            probeAllButtons()
            // 本地 API：隧道要用它做被映射的本地端口，也供服务端探活。
            append("localapi.start → \(AgovBridge.invoke("localapi.start", "{\"port\":18989}"))")
        }
    }

    /// 追加一行日志：同时写进界面列表和 Documents/app.log。
    ///
    /// 为什么要落盘：界面上只能看到前若干行，CI 也拿不到屏幕上的文字；
    /// 写到 Documents 后，CI 用 simctl get_app_container 就能把完整日志取出来当 artifact。
    private func append(_ s: String) {
        log.append(LogLine(text: s))
        if log.count > 200 { log.removeFirst(log.count - 200) }
        appendToFile(s)
    }

    /// resetLogFile 每次启动清空 app.log，避免多次运行混在一起。
    private func resetLogFile() {
        guard let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first else { return }
        try? FileManager.default.removeItem(at: docs.appendingPathComponent("app.log"))
    }

    /// appendToFile 把一行追加到 Documents/app.log（超过 512 KB 自动截断重写）。
    private func appendToFile(_ line: String) {
        guard let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first else { return }
        let url = docs.appendingPathComponent("app.log")
        let text = line + "\n"
        guard let data = text.data(using: .utf8) else { return }
        if let handle = try? FileHandle(forWritingTo: url) {
            defer { try? handle.close() }
            let size = (try? handle.seekToEnd()) ?? 0
            if size < 512 * 1024 { try? handle.write(contentsOf: data) }
        } else {
            try? data.write(to: url)
        }
    }

    /// Web 控制台的地址：本地 HTTP 服务提供的 /ui/ 页面（可热更）。
    @State private var showWebConsole = false
    /// 启动流程只跑一次（SwiftUI 的 onAppear 会重复触发）。
    @State private var didStartup = false
    private let consoleURL = URL(string: "http://127.0.0.1:18989/ui/index.html")!

    private func callCapabilities() {
        append("capabilities → \(AgovBridge.invoke("capabilities", "{}"))")
    }

    /// 自检：真投一次点击 + 真抓一帧，把"提交结果"和"可观测结果"分开报。
    /// 与"能力探测"的区别：能力探测只看符号在不在，自检看做了之后有没有反应。
    private func callSelfTest() {
        append("runtime.selftest → \(AgovBridge.invoke("runtime.selftest", "{\"x\":0.5,\"y\":0.5}"))")
    }

    private func callDeviceInfo() {
        append("device.info → \(AgovBridge.invoke("device.info", "{}"))")
    }

    private func callScreenshot() {
        let r = AgovBridge.invoke("capture.screenshot",
                                  "{\"maxSize\":1024,\"jpeg\":true,\"quality\":0.8}")
        append("capture.screenshot → \(String(r.prefix(160)))")
        if let data = AgovBridge.lastImageData {
            image = UIImage(data: data)
            imageInfo = "\(AgovBridge.lastImageWidth)x\(AgovBridge.lastImageHeight)"
            // 落盘到 Documents：CI 用 simctl get_app_container 取这个目录当 artifact，
            // 这样"图"和"日志"能一起下载，不用挤在截图里看。
            let url = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
                .appendingPathComponent("capture.jpg")
            try? data.write(to: url)
            append("截图已保存 \(data.count) 字节 → \(url.path)")
        }
    }

    /// 跑端上脚本：走 yaegi 解释器，脚本内容来自 App 内置示例。
    /// 这一步通了，就说明"改脚本不重装"的热更新链路成立。
    private func runScript() {
        let r = AgovBridge.invoke("script.run", "{\"name\":\"demo\",\"x\":0.5,\"y\":0.5}")
        append("script.run → \(String(r.prefix(600)))")
    }

    /// frps 地址：诊断版先写死，接入真实环境时改成配置项。
    private let frpsHost = "frps.example.com"

    /// 启动内网穿透：把本地 18989 映射到 frps 上。
    private func startTunnel() {
        let json = "{\"server\":\"\(frpsHost)\",\"serverPort\":7000,\"localPort\":18989,\"name\":\"agov-demo\"}"
        append("tunnel.start → \(String(AgovBridge.invoke("tunnel.start", json).prefix(400)))")
    }

    private func stopTunnel() {
        append("tunnel.stop → \(String(AgovBridge.invoke("tunnel.stop", "{}").prefix(200)))")
    }

    private func tunnelStatus() {
        append("tunnel.status → \(String(AgovBridge.invoke("tunnel.status", "{}").prefix(400)))")
    }

    private func injectTap() {
        append("motion.tap(0.5,0.5) → \(AgovBridge.invoke("motion.tap", "{\"x\":0.5,\"y\":0.5}"))")
    }

    private func injectSwipe() {
        append("motion.swipe → \(AgovBridge.invoke("motion.swipe", "{\"x1\":0.5,\"y1\":0.75,\"x2\":0.5,\"y2\":0.35,\"durationMs\":400}"))")
    }
    /// 把界面上每个按钮对应的 op 各调一次，输出一行结果。
    ///
    /// 为什么放在启动时：模拟器里没法点按钮（simctl 不提供点击），
    /// 而 CI 只能截图；启动时跑一遍才能在一张图里看到全部状态。
    private func probeAllButtons() {
        append("—— 按钮探测开始 ——")
        let probes: [(String, String, String)] = [
            ("设备信息",      "device.info",  "{}"),
            ("截图",          "capture.screenshot", "{\"maxSize\":512,\"jpeg\":true,\"quality\":0.6}"),
            ("隧道状态",      "tunnel.status", "{}"),
            ("启动穿透",      "tunnel.start", "{\"server\":\"frps.example.com\",\"localPort\":18989}"),
            ("注入点击(中心)", "motion.tap",   "{\"x\":0.5,\"y\":0.5}"),
            ("注入上滑",      "motion.swipe", "{\"x1\":0.5,\"y1\":0.75,\"x2\":0.5,\"y2\":0.35,\"durationMs\":300}"),
            ("SDK 状态",      "sdk.status",   "{}"),
        ]
        for (label, method, payload) in probes {
            let result = AgovBridge.invoke(method, payload)
            let short = result.count > 200 ? String(result.prefix(200)) + "…" : result
            append("\(label) → \(short)")
        }
        append("—— 按钮探测结束 ——")
    }
}
