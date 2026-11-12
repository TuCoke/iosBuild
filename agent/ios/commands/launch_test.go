package commands

import (
	"autogov1/v2/protocol"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type launchBackend struct {
	testBackend
	launches, queries int
	match             bool
}

func (b *launchBackend) Invoke(ctx context.Context, n string, a any, o any) error {
	if n == "app.launch" {
		b.launches++
		return json.Unmarshal([]byte(`{}`), o)
	}
	if n == "app.foreground" {
		b.queries++
		fg := "com.apple.springboard"
		if b.match && b.queries > 1 {
			fg = "com.apple.weather"
		}
		v, _ := json.Marshal(fg)
		return json.Unmarshal(v, o)
	}
	return b.testBackend.Invoke(ctx, n, a, o)
}
func TestLaunchConfirmationDoesNotReplay(t *testing.T) {
	for _, match := range []bool{true, false} {
		b := &launchBackend{match: match}
		d := New(b)
		ctx, cancel := context.WithTimeout(context.Background(), 650*time.Millisecond)
		_, e := d.Execute(ctx, protocol.Cmd{Cmd: "open_app", Pkg: "com.apple.weather"})
		cancel()
		if b.launches != 1 || b.queries < 2 {
			t.Fatalf("launches=%d queries=%d", b.launches, b.queries)
		}
		if match && e != nil {
			t.Fatal(e)
		}
		if !match && (e == nil || d.Unknown(e) || !strings.Contains(e.Error(), "com.apple.springboard")) {
			t.Fatalf("%v", e)
		}
	}
}
