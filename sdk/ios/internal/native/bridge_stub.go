//go:build !ios || !cgo

package native

// 非 iOS 或没有 cgo 时保留可导入性，但所有设备调用明确失败。
func invoke(op string, _ []byte) (Result, error) {
	return Result{}, statusError(op, 1, "需要真实 iOS 设备和启用 cgo 的构建")
}
