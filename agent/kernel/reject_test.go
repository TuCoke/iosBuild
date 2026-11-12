package kernel

import (
	"autogov1/v2/protocol"
	"testing"
)

func TestRejectBeforeStartPersistsReason(t *testing.T) {
	dir := t.TempDir()
	j, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = j.Reserve(protocol.Cmd{ID: "test-reject", Cmd: "wake"}); e != nil {
		t.Fatal(e)
	}
	r, e := j.RejectBeforeStart("test-reject", "栅栏版本落后")
	if e != nil || r.State != "rejected" || r.Msg != "栅栏版本落后" {
		t.Fatalf("%+v %v", r, e)
	}
	j, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	r, _, e = j.Reserve(protocol.Cmd{ID: "test-reject", Cmd: "wake"})
	if e != nil || r.State != "rejected" || r.Msg != "栅栏版本落后" {
		t.Fatalf("%+v %v", r, e)
	}
}
