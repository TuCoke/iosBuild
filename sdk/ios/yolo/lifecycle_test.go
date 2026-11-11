package yolo

import (
	"autogov1/v2/sdk/ios/types"
	"errors"
	"image"
	"testing"
)

func TestClosedEngineCannotLoadAgain(t *testing.T) {
	y := New("v8", 1, "model.param", "model.bin", "a,b")
	y.Close()
	if got := y.DetectFromImage(image.NewNRGBA(image.Rect(0, 0, 1, 1))); got != nil || !errors.Is(y.Error(), types.ErrClosed) {
		t.Fatal(got, y.Error())
	}
	y.Close()
}
