// 应用管理：启动、强杀、安装/卸载/清数据、包信息与图标。
//
// 【安装/卸载/清数据】iOS 上要走 MobileInstallation 私有服务，与 Android 的
// PackageManager 对应；要求 platform-application 权限（TrollStore/越狱环境），
// 普通签名调用会被系统拒绝并返回 false。

package app

import (
	"time"

	"autogov1/v2/sdk/ios/internal/bridge"
)

// AppInfo 与 AutoGo 字段完全一致。
type AppInfo struct {
	PackageName string // 包名（bundleIdentifier）
	AppName     string // 应用显示名称
	VersionName string // 版本号
	VersionCode string // 构建号
	IsSystemApp bool   // 是否系统应用
}

// CurrentPackage 返回当前前台应用包名；取不到返回空串。
func CurrentPackage() string {
	var out struct {
		BundleID string `json:"bundleId"`
	}
	if !bridge.Do("app.foreground", nil, &out) {
		return ""
	}
	return out.BundleID
}

// Launch 启动应用；失败返回 false（不存在、被限制、权限不足都会失败）。
func Launch(packageName string) bool {
	return bridge.Do("app.launch", map[string]any{"bundleId": packageName}, nil)
}

// ForceStop 强杀应用。
func ForceStop(packageName string) {
	_ = bridge.Do("app.stop", map[string]any{"bundleId": packageName}, nil)
}

// GetBundlePath 返回 .app 的沙盒路径。
func GetBundlePath(packageName string) string {
	var out struct {
		Path string `json:"path"`
	}
	if !bridge.Do("app.bundle_path", map[string]any{"bundleId": packageName}, &out) {
		return ""
	}
	return out.Path
}

// GetDataPath 返回应用数据容器路径。
func GetDataPath(packageName string) string {
	var out struct {
		Path string `json:"path"`
	}
	if !bridge.Do("app.data_path", map[string]any{"bundleId": packageName}, &out) {
		return ""
	}
	return out.Path
}

// GetList 列出已安装应用；includeSystemApps=false 时过滤系统应用。
func GetList(includeSystemApps bool) []AppInfo {
	var out struct {
		Items []AppInfo `json:"items"`
	}
	if !bridge.DoTimeout("app.list_all", map[string]any{"system": includeSystemApps}, &out, 30*time.Second) {
		return nil
	}
	return out.Items
}

// SelfPackage 返回本应用自己的 bundleID。
func SelfPackage() string {
	var out struct {
		BundleID string `json:"bundleId"`
	}
	if !bridge.Do("app.self", nil, &out) {
		return ""
	}
	return out.BundleID
}

// GetName 返回应用显示名。
func GetName(packageName string) string {
	var out struct {
		Name string `json:"name"`
	}
	if !bridge.Do("app.info", map[string]any{"bundleId": packageName, "field": "name"}, &out) {
		return ""
	}
	return out.Name
}

// GetVersion 返回版本号（CFBundleShortVersionString）。
func GetVersion(packageName string) string {
	var out struct {
		Version string `json:"version"`
	}
	if !bridge.Do("app.info", map[string]any{"bundleId": packageName, "field": "version"}, &out) {
		return ""
	}
	return out.Version
}

// GetIcon 返回应用图标的 PNG 字节。
func GetIcon(packageName string) []byte {
	return bridge.Raw("app.icon", map[string]any{"bundleId": packageName})
}

// IsInstalled 判断是否已安装。
func IsInstalled(packageName string) bool {
	var out struct {
		Installed bool `json:"installed"`
	}
	if !bridge.Do("app.installed", map[string]any{"bundleId": packageName}, &out) {
		return false
	}
	return out.Installed
}

// Uninstall 卸载应用（不可逆）。
func Uninstall(packageName string) {
	_ = bridge.DoTimeout("app.uninstall", map[string]any{"bundleId": packageName}, nil, 60*time.Second)
}

// Install 安装 ipa/包路径。
func Install(path string) {
	_ = bridge.DoTimeout("app.install", map[string]any{"path": path}, nil, 2*time.Minute)
}

// Clear 清空应用数据（不可逆）。
func Clear(packageName string) {
	_ = bridge.DoTimeout("app.clear", map[string]any{"bundleId": packageName}, nil, 60*time.Second)
}

// OpenUrl 用系统打开一个 URL 或 URL Scheme。
func OpenUrl(url string) {
	_ = bridge.Do("app.open_url", map[string]any{"url": url}, nil)
}
