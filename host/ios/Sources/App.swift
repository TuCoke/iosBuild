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
                Button("设备信息") { callDeviceInfo() }
                Button("截图") { callScreenshot() }
            }
            .buttonStyle(BorderlessButtonStyle())
            .padding(.horizontal, 12)

            HStack(spacing: 8) {
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
        .onAppear {
            append("AgovHost 启动")
            callCapabilities()
        }
    }

    private func append(_ s: String) {
        log.append(LogLine(text: s))
        if log.count > 200 { log.removeFirst(log.count - 200) }
    }

    private func callCapabilities() {
        append("capabilities → \(AgovBridge.invoke("capabilities", "{}"))")
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
        }
    }

    private func injectTap() {
        append("motion.tap(0.5,0.5) → \(AgovBridge.invoke("motion.tap", "{\"x\":0.5,\"y\":0.5}"))")
    }

    private func injectSwipe() {
        append("motion.swipe → \(AgovBridge.invoke("motion.swipe", "{\"x1\":0.5,\"y1\":0.75,\"x2\":0.5,\"y2\":0.35,\"durationMs\":400}"))")
    }
}
