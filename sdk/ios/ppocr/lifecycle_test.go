package ppocr

import (
	"autogov1/v2/sdk/ios/types"
	"errors"
	"image"
	"testing"
)

func TestClosedEngineCannotLoadAgain(t *testing.T) {
	p := New("v5")
	p.Close()
	if got := p.OcrFromImage(image.NewNRGBA(image.Rect(0, 0, 1, 1)), ""); got != nil || !errors.Is(p.Error(), types.ErrClosed) {
		t.Fatal(got, p.Error())
	}
	p.Close()
}
