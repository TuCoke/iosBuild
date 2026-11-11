//go:build !ios || !cgo

package imgui

import (
	"autogov1/v2/sdk/ios/types"
	"context"
)

func Run(ctx context.Context, draw func()) error                      { return types.ErrUnsupported }
func Checkbox(label string, value *bool) bool                         { panic(types.ErrUnsupported) }
func SliderFloat(label string, value *float32, min, max float32) bool { panic(types.ErrUnsupported) }
func SliderInt(label string, value *int, min, max int) bool           { panic(types.ErrUnsupported) }
func InputText(label string, value *string, capacity int) bool        { panic(types.ErrUnsupported) }
