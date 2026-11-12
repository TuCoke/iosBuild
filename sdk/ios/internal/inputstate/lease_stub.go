//go:build !ios || !cgo

package inputstate

import "context"

// 非 iOS 平台只用于纯 Go 验证，不假装拥有可操作的 iOS 设备锁。
func acquireDevice(ctx context.Context) error { return ctx.Err() }
func releaseDevice()                          {}
