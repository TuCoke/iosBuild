// Package imgui 提供 Dear ImGui / cimgui 的 Go 脚本 UI 绑定。
// Run 绑定宿主主线程的 Metal 帧，包含基础窗口、44 项生成控件/布局接口与可变值控件。
// 控件、表格和弹窗应按 ImGui 约定配对；任务取消后释放回调，回调 panic 会返回错误。
// 生成器仅覆盖已经审核的 ABI，不把全部 5,468 个编译名称当作已实现的 Go API。
// 当前 vendor 为 1.92.5 / 19250；样本记录为 1.92.5 WIP / 19244。
// 本实现依附宿主 WindowScene，独立跨 App 全局悬浮宿主仍需另行实现。
package imgui
