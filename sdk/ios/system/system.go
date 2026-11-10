// Package system 对齐 AutoGo iOS 的系统 API（4 个函数）。
//
// 全部走原生桥：进程枚举、内存/CPU 采样在 iOS 上要 sysctl / host_statistics64 /
// proc_pidinfo 这些私有入口，纯 Go 拿不到（Go 的 runtime 只能看到自己）。
//
// 【失败返回零值】与 AutoGo 一致：查不到进程返回 0，采不到占用返回 0。
package system

import (
	"time"

	"autogov1/v2/sdk/ios/internal/bridge"
)

// GetPid 按进程名找 pid；找不到返回 0。
func GetPid(processName string) int {
	var out struct {
		PID int `json:"pid"`
	}
	if !bridge.Do("system.pid", map[string]any{"name": processName}, &out) {
		return 0
	}
	return out.PID
}

// GetMemoryUsage 返回该进程的常驻内存（KB）。
func GetMemoryUsage(pid int) int {
	var out struct {
		KB int `json:"kb"`
	}
	if !bridge.Do("system.memory", map[string]any{"pid": pid}, &out) {
		return 0
	}
	return out.KB
}

// GetCpuUsage 返回该进程的 CPU 占用百分比（0~100）。
func GetCpuUsage(pid int) float64 {
	var out struct {
		Percent float64 `json:"percent"`
	}
	if !bridge.Do("system.cpu", map[string]any{"pid": pid}, &out) {
		return 0
	}
	return out.Percent
}

// RestartSelf 重启自身进程。原生侧通常是「拉起新实例后 exit(0)」，
// 所以这个调用不该返回；给一点时间让原生完成交接。
func RestartSelf() {
	success := bridge.Do("system.restart_self", map[string]any{}, nil)
	if success {
		time.Sleep(1500 * time.Millisecond)
	}
}
