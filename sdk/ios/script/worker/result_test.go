package worker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkerResultLimitsAndSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, []byte(`{"ok":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if raw, err := readWorkerResult(path); err != nil || string(raw) != `{"ok":true}` {
		t.Fatal(string(raw), err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(maxResultBytes + 1)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readWorkerResult(path); err == nil {
		t.Fatal("超大结果未拒绝")
	}
	if _, err := readWorkerResult(dir); err == nil {
		t.Fatal("目录未拒绝")
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err == nil {
		if _, err := readWorkerResult(link); err == nil {
			t.Fatal("结果软链接未拒绝")
		}
	}
}
