package runtime

import (
	"autogov1/v2/agent/kernel"
	"path/filepath"
	"testing"
)

func TestCloudJournalsKeepIndependentFences(t *testing.T) {
	root := t.TempDir()
	a, e := openCloudJournal(Config{Server: "wss://a.example/agent", DataDir: root})
	if e != nil {
		t.Fatal(e)
	}
	if e = a.AcceptFence(235, "old"); e != nil {
		t.Fatal(e)
	}
	b, e := openCloudJournal(Config{Server: "wss://b.example/agent", DataDir: root})
	if e != nil {
		t.Fatal(e)
	}
	if e = b.AcceptFence(1, "new"); e != nil {
		t.Fatal(e)
	}
	a, e = openCloudJournal(Config{Server: "wss://a.example/agent", DataDir: root})
	if e != nil {
		t.Fatal(e)
	}
	if e = a.AcceptFence(234, "late"); e == nil {
		t.Fatal("重新连接必须保留原栅栏")
	}
	old, e := kernel.Open(filepath.Join(root, "journal"))
	if e != nil {
		t.Fatal(e)
	}
	if e = old.AcceptFence(5, "legacy"); e != nil {
		t.Fatal(e)
	}
	if _, e = openCloudJournal(Config{Server: "wss://a.example/agent", DataDir: root}); e == nil {
		t.Fatal("旧日志不能静默丢弃")
	}
}
