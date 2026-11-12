package runtime

import (
	"autogov1/v2/sdk/ios/types"
	"context"
	"errors"
	"testing"
)

func TestStructuredEntryRejectsProcessAndBinaryMethods(t *testing.T) {
	for _, method := range []string{"utils.spawn", "capture.image", "video.read", "app.uninstall"} {
		if _, e := CallJSON(context.Background(), method, nil); !errors.Is(e, types.ErrUnsupported) {
			t.Fatalf("结构化入口接受了不允许的方法 %s: %v", method, e)
		}
	}
}
