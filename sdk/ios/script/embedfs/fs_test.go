package embedfs

import (
	"testing"
	"testing/fstest"
)

func TestResourceFileSystem(t *testing.T) {
	filesystem := New(map[string]string{"assets/a.txt": "hello", "assets/nested/b.txt": "world"})
	if err := fstest.TestFS(filesystem, "assets/a.txt", "assets/nested/b.txt"); err != nil {
		t.Fatal(err)
	}
}
