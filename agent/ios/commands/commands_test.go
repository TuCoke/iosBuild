package commands

import (
	"autogov1/v2/agent/ios/runtime"
	"autogov1/v2/core/gesture"
	"autogov1/v2/protocol"
	"context"
	"encoding/json"
	"testing"
)

type testBackend struct {
	calls []string
	args  []any
}

func (b *testBackend) Invoke(_ context.Context, n string, a any, o any) error {
	b.calls = append(b.calls, n)
	b.args = append(b.args, a)
	if o != nil {
		return json.Unmarshal([]byte(`{}`), o)
	}
	return nil
}
func (*testBackend) Info(context.Context) (runtime.Info, error) { return runtime.Info{}, nil }
func (*testBackend) Healthy(context.Context) error              { return nil }
func (*testBackend) Screenshot(context.Context) ([]byte, error) { return nil, nil }
func (*testBackend) OpenVideo(context.Context, runtime.VideoOptions) (runtime.VideoSource, error) {
	return nil, nil
}
func TestIOSOnlyGestureAndUnsupportedBack(t *testing.T) {
	b := &testBackend{}
	d := New(b)
	if _, e := d.Execute(context.Background(), protocol.Cmd{Cmd: "key", Key: 4}); e == nil || len(b.calls) != 0 {
		t.Fatal("Android Back 进入了 iOS 驱动")
	}
	frames := []gesture.Frame{{AtMS: 0, Contacts: []gesture.Contact{{ID: 0, X: .2, Y: .3}, {ID: 1, X: .8, Y: .3}}}, {AtMS: 20, Contacts: []gesture.Contact{}}}
	if _, e := d.Execute(context.Background(), protocol.Cmd{Cmd: "gesture", Frames: frames}); e != nil {
		t.Fatal(e)
	}
	if len(b.calls) != 1 || b.calls[0] != "motion.gesture" {
		t.Fatal("多指轨迹未进入 iOS SDK")
	}
	force := .5
	frames[0].Contacts[0].Pressure = &force
	if _, e := d.Execute(context.Background(), protocol.Cmd{Cmd: "gesture", Frames: frames}); e == nil || len(b.calls) != 1 {
		t.Fatal("不支持的压力字段被静默忽略")
	}
}
