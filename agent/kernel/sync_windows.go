//go:build windows

package kernel

// Windows 主机只用于编译日志组件；实际 Android 执行使用目录同步实现。
func syncDirectory(string) error { return nil }
