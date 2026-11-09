// Package app 封装 iOS 应用启动、查询，不把安装或提权工具混入 SDK。
package app

import (
	"context"
	"fmt"
	"strings"

	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/types"
)

type Info struct {
	BundleID string `json:"bundleId"`
	Name     string `json:"name"`
	Version  string `json:"version"`
}

func Launch(ctx context.Context, bundleID string) error {
	if strings.TrimSpace(bundleID) == "" || strings.ContainsAny(bundleID, "\x00\r\n") {
		return fmt.Errorf("%w: bundleID 无效", types.ErrInvalid)
	}
	return native.Call(ctx, "app.launch", map[string]any{"bundleId": bundleID}, nil)
}
func Foreground(ctx context.Context) (string, error) {
	var v struct {
		BundleID string `json:"bundleId"`
	}
	err := native.Call(ctx, "app.foreground", nil, &v)
	return v.BundleID, err
}
func List(ctx context.Context) ([]Info, error) {
	var v []Info
	err := native.Call(ctx, "app.list", nil, &v)
	return v, err
}
