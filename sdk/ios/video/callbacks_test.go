package video

import (
	"autogov1/v2/sdk/ios/types"
	"errors"
	"testing"
)

func TestRejectInvalidCallbackConfiguration(t *testing.T) {
	if !errors.Is(SetEncoderParams(960, 1, 30), types.ErrInvalid) {
		t.Fatal("接受了非法码率")
	}
	if !errors.Is(RawFrameStart(nil), types.ErrInvalid) {
		t.Fatal("接受了空回调")
	}
}
