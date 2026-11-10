// 键码与按键动作。
//
// 键码沿用 Android 的编号（AutoGo 的可见 API 就是这样，iOS 侧由原生映射到 HID usage），
// 这样同一份脚本在两端都能写 KEYCODE_HOME / KEYCODE_ENTER。
package motion

import (
	"context"
	"time"

	"autogov1/v2/sdk/ios/internal/native"
)

const (
	KEYCODE_HOME        = 3
	KEYCODE_DPAD_UP     = 19
	KEYCODE_DPAD_DOWN   = 20
	KEYCODE_DPAD_LEFT   = 21
	KEYCODE_DPAD_RIGHT  = 22
	KEYCODE_VOLUME_UP   = 24
	KEYCODE_VOLUME_DOWN = 25
	KEYCODE_POWER       = 26
	KEYCODE_TAB         = 61
	KEYCODE_SPACE       = 62
	KEYCODE_ENTER       = 66
	KEYCODE_DEL         = 67 // Backspace
	KEYCODE_SEARCH      = 84 // 等同于 Enter（iOS 搜索框中回车即搜索）
	KEYCODE_ESCAPE      = 111
	KEYCODE_FORWARD_DEL = 112
	KEYCODE_APP_SWITCH  = 187
)

// Home 按下 Home 键（回到桌面）。
func Home() { KeyAction(KEYCODE_HOME) }

// Recents 打开最近任务。
func Recents() { KeyAction(KEYCODE_APP_SWITCH) }

// VolumeUp 音量加。
func VolumeUp() { KeyAction(KEYCODE_VOLUME_UP) }

// VolumeDown 音量减。
func VolumeDown() { KeyAction(KEYCODE_VOLUME_DOWN) }

// KeyAction 按下任意键码（用上面的 KEYCODE_* 常量）。
func KeyAction(code int) {
	if code <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = native.Call(ctx, "motion.key", map[string]any{"code": code}, nil)
}
