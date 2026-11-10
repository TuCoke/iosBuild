// 文本输入与剪贴板。
//
// 这里给的是"阻塞到打完"的简版 InputText 和剪贴板读写；
// 需要可取消/可分批的版本用 InputTextContext（ime.go）。

package ime

import (
	"context"
	"time"

	"autogov1/v2/sdk/ios/internal/bridge"
)

// InputText 输入一段文本（阻塞到打完为止；失败静默，与 AutoGo 一致）。
func InputText(text string) {
	if text == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_ = InputTextContext(ctx, text)
}

// GetClipText 读系统剪贴板；失败返回空串。
func GetClipText() string {
	var out struct {
		Text string `json:"text"`
	}
	if !bridge.Do("device.clipget", nil, &out) {
		return ""
	}
	return out.Text
}

// SetClipText 写系统剪贴板。
func SetClipText(text string) bool {
	return bridge.Do("device.clipset", map[string]any{"text": text}, nil)
}
