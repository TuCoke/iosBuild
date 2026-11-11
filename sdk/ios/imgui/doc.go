// Package imgui 提供 Dear ImGui / cimgui 的基础 Go 绑定。
//
// 当前已有 12 个基础函数，供 HUD/Console 的 ImGui + Metal 面板使用；
// 不是完整生成绑定，不能把原生 cimgui 的全部导出符号当成 Go API 已覆盖。
// Button、输入框、表格等完整控件、结构访问器与脚本绑定仍需生成和封装。
//
// 当前 vendor 为 1.92.5 / 19250；样本有 1.92.5 WIP / 19244 的版本记录，
// 不宣称具体版本及全部接口完全相同。当前面板依附宿主 WindowScene，
// 独立的跨 App 全局悬浮宿主仍未实现。当前状态见 docs/ios-autogo-status.md。
package imgui
