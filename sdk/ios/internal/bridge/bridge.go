// Package bridge 给"无 context、出错返回零值"的简版 API 用。
//
// 为什么单独一层：简版 API 不返回 error（读不到就返回零值），
// 我们要做到同名同参同返回，就不能把 context 和 error 摆到台面上；
// 但内部仍然走 native.Call 那套可取消、可分类的边界。
// 这里统一给 10 秒超时，避免原生卡住把调用方一起拖死。
package bridge

import (
	"context"
	"time"

	"autogov1/v2/sdk/ios/internal/native"
)

const defaultTimeout = 10 * time.Second

// Do 调一个原生方法；失败只返回 false，由调用方给零值。
func Do(op string, input, output any) bool {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	return native.Call(ctx, op, input, output) == nil
}

// DoTimeout 同上，超时由调用方指定（下载、OCR 这类慢操作）。
func DoTimeout(op string, input, output any, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return native.Call(ctx, op, input, output) == nil
}

// Raw 直接拿二进制结果（截图、图标这类）。
func Raw(op string, input any) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	r, err := native.Raw(ctx, op, input)
	if err != nil {
		return nil
	}
	return r.Data
}
