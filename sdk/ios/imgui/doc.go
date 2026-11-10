// Package imgui 是 Dear ImGui（cimgui）的 Go 绑定占位。
//
// 【为什么是占位】AutoGo 的 iOS imgui 包有 4835 个导出符号 —— 它们不是手写的，
// 是用 cimgui 的 C API（imgui.h / cimgui.h）批量生成的绑定。手抄没有意义，
// 而且会立刻和上游版本漂移。
//
// 生成方式（与 AutoGo 同一套）：
//  1. 原生：把 cimgui 编成 iOS arm64 dylib（libimgui.dylib，AutoGo 那份 1.8 MB）
//  2. 绑定：用 cimgui 的 C 头做机械转换，一个 C 函数对应一个 Go 函数，
//     命名沿用 ImGui 原名（Begin/End/Text/Button…），参数按 C 类型映射
//  3. 生成物放 imgui/gen_*.go，不要手改
//
// 目前 SDK 里没有任何业务代码依赖 imgui：我们的窗口/悬浮 UI 走 WebView 与服务端控制台。
// 等要做"设备端悬浮控制台"时再按上面的流程生成，避免现在引入 4 万行无法维护的绑定。
package imgui
