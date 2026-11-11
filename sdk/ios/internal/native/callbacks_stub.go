//go:build !ios || !cgo

package native

import "autogov1/v2/sdk/ios/types"

func H264CallbackSet(func([]byte)) error               { return types.ErrUnsupported }
func RawCallbackSet(func([]byte, int, int, int)) error { return types.ErrUnsupported }
func CallbackError() error                             { return types.ErrUnsupported }
func EncoderParams(int, int, int) error                { return types.ErrUnsupported }
func EncoderStart() error                              { return types.ErrUnsupported }
func EncoderStop() error                               { return types.ErrUnsupported }
func RawStart() error                                  { return types.ErrUnsupported }
func RawStop() error                                   { return types.ErrUnsupported }
