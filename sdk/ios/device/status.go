// 设备状态与开关：电量、内存、亮屏/解锁、亮度、VPN、WiFi、IP/MAC、重启。
//
// 这些几乎都要私有入口：电量/亮屏要 IOKit，内存要 host_statistics64，
// IP/MAC 要 ifaddrs + 私有字段，VPN/WiFi 开关要 networkd 授权。统一走原生桥。

package device

import (
	"time"

	"autogov1/v2/sdk/ios/internal/bridge"
)

// GetDisplayInfo 返回 (宽, 高, 缩放, 旋转)。取不到返回全 0。
func GetDisplayInfo() (width, height int, scale float64, rotation int) {
	var out struct {
		Width    int     `json:"width"`
		Height   int     `json:"height"`
		Scale    float64 `json:"scale"`
		Rotation int     `json:"rotation"`
	}
	if !bridge.Do("device.display", nil, &out) {
		return 0, 0, 0, 0
	}
	return out.Width, out.Height, out.Scale, out.Rotation
}

// GetBattery 返回电量百分比（0~100）。
func GetBattery() int {
	var out struct {
		Level int `json:"level"`
	}
	if !bridge.Do("device.battery", nil, &out) {
		return 0
	}
	return out.Level
}

// GetBatteryStatus 返回充电状态：0 未知、1 放电、2 充电、3 满电（与 AutoGo 一致）。
func GetBatteryStatus() int {
	var out struct {
		Status int `json:"status"`
	}
	if !bridge.Do("device.battery_status", nil, &out) {
		return 0
	}
	return out.Status
}

// IsScreenOn 屏幕是否点亮。
func IsScreenOn() bool {
	var out struct {
		On bool `json:"on"`
	}
	if !bridge.Do("device.screen_on", nil, &out) {
		return false
	}
	return out.On
}

// IsScreenUnlock 是否已解锁（非锁屏态）。
func IsScreenUnlock() bool {
	var out struct {
		Unlocked bool `json:"unlocked"`
	}
	if !bridge.Do("device.unlocked", nil, &out) {
		return false
	}
	return out.Unlocked
}

// GetBrightness 返回亮度百分比（0~100）。
func GetBrightness() int {
	var out struct {
		Value int `json:"value"`
	}
	if !bridge.Do("device.brightness_get", nil, &out) {
		return 0
	}
	return out.Value
}

// GetTotalMem 总内存（MB）。
func GetTotalMem() int {
	var out struct {
		Total int `json:"total"`
	}
	if !bridge.Do("device.memory", map[string]any{"field": "total"}, &out) {
		return 0
	}
	return out.Total
}

// GetAvailMem 可用内存（MB）。
func GetAvailMem() int {
	var out struct {
		Avail int `json:"avail"`
	}
	if !bridge.Do("device.memory", map[string]any{"field": "avail"}, &out) {
		return 0
	}
	return out.Avail
}

// VpnStatus 是否已连 VPN。
func VpnStatus() bool {
	var out struct {
		On bool `json:"on"`
	}
	if !bridge.Do("device.vpn_status", nil, &out) {
		return false
	}
	return out.On
}

// SetVPNEnabled 开关 VPN；没有权限时返回 false。
func SetVPNEnabled(enabled bool) bool {
	var out struct {
		OK bool `json:"ok"`
	}
	if !bridge.DoTimeout("device.vpn_set", map[string]any{"enabled": enabled}, &out, 30*time.Second) {
		return false
	}
	return out.OK
}

// SetWiFiEnabled 开关 WiFi；没有权限时返回 false。
func SetWiFiEnabled(enabled bool) bool {
	var out struct {
		OK bool `json:"ok"`
	}
	if !bridge.DoTimeout("device.wifi_set", map[string]any{"enabled": enabled}, &out, 30*time.Second) {
		return false
	}
	return out.OK
}

// WakeUp 点亮屏幕。
func WakeUp() { _ = bridge.Do("device.wake", nil, nil) }

// KeepScreenOn 保持屏幕常亮（防止自动锁屏打断脚本）。
func KeepScreenOn() { _ = bridge.Do("device.keep_awake", map[string]any{"on": true}, nil) }

// GetIp 返回本机局域网 IP。
func GetIp() string {
	var out struct {
		IP string `json:"ip"`
	}
	if !bridge.Do("device.ip", nil, &out) {
		return ""
	}
	return out.IP
}

// GetWifiMac 返回 WiFi MAC（iOS 只给随机化地址，未必是硬件 MAC）。
func GetWifiMac() string {
	var out struct {
		MAC string `json:"mac"`
	}
	if !bridge.Do("device.wifi_mac", nil, &out) {
		return ""
	}
	return out.MAC
}

// Reboot 重启设备（需要更高权限；调用后通常不返回）。
func Reboot() {
	_ = bridge.DoTimeout("device.reboot", nil, nil, 30*time.Second)
}
