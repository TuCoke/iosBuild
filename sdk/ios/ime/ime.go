// Package ime 提供文本和 HID 键盘输入，不切换安卓输入法。
package ime

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"autogov1/v2/sdk/ios/internal/inputstate"
	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/types"
)

// InputTextContext 按系统组合字符边界提交 UTF-8 数据；取消时返回已经提交的事实，不自动重发。
// （简版 InputText 在 text.go。）
func InputTextContext(ctx context.Context, text string) error {
	if !utf8.ValidString(text) || len(text) > 65536 {
		return fmt.Errorf("%w: 文本必须是有效 UTF-8 且不超过 64 KiB", types.ErrInvalid)
	}
	if ctx == nil {
		return types.ErrInvalid
	}
	if err := inputstate.Acquire(ctx); err != nil {
		return err
	}
	defer inputstate.Release()
	sent := false
	var parts struct {
		Segments []string `json:"segments"`
	}
	if text == "" {
		return nil
	}
	if err := native.Call(ctx, "ime.segments", map[string]any{"text": text}, &parts); err != nil {
		return err
	}
	for i, segment := range parts.Segments {
		if err := ctx.Err(); err != nil {
			if sent {
				return &types.Error{Op: "ime.text", Kind: types.ErrUnknown, Detail: "部分文字已提交: " + err.Error()}
			}
			return err
		}
		if err := native.Call(ctx, "ime.text", map[string]any{"text": segment}, nil); err != nil {
			if sent {
				return &types.Error{Op: "ime.text", Kind: types.ErrUnknown, Detail: "部分文字已提交: " + err.Error()}
			}
			return err
		}
		sent = true
		if i == len(parts.Segments)-1 {
			break
		}
		if (i+1)%4 != 0 {
			continue
		}
		t := time.NewTimer(15 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return &types.Error{Op: "ime.text", Kind: types.ErrUnknown, Detail: "文字已提交，输入被取消"}
		case <-t.C:
		}
	}
	return nil
}

// Key 使用 USB HID Usage Page 0x07 的键值，不能直接传安卓 keycode。
func Key(ctx context.Context, usage uint16) error {
	if usage == 0 || usage > 0xe7 {
		return fmt.Errorf("%w: HID 键值无效", types.ErrInvalid)
	}
	if err := inputstate.Acquire(ctx); err != nil {
		return err
	}
	defer inputstate.Release()
	return native.Call(ctx, "ime.key", map[string]any{"usage": usage}, nil)
}
