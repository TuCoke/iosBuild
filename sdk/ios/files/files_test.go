package files

import (
	"bytes"
	"howett.net/plist"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// newRoot 把包级根目录指到临时目录，避免测试污染真实沙盒。
func newRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	SetRoot(dir)
	return dir
}

func TestWriteReadAppend(t *testing.T) {
	root := newRoot(t)
	Write("a.txt", "hello")
	if got := Read("a.txt"); got != "hello" {
		t.Fatalf("Read = %q", got)
	}
	Append("a.txt", "-world")
	if got := Read("a.txt"); got != "hello-world" {
		t.Fatalf("Append 后 Read = %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); err != nil {
		t.Fatalf("文件没写到沙盒根: %v", err)
	}
}

func TestReadMissingReturnsEmpty(t *testing.T) {
	newRoot(t)
	if got := Read("nope.txt"); got != "" {
		t.Fatalf("读不存在的文件应返回空串，实际 %q", got)
	}
	if Exists("nope.txt") {
		t.Fatal("Exists 不应为 true")
	}
}

func TestEnsureDirAndList(t *testing.T) {
	newRoot(t)
	if !EnsureDir("x/y") {
		t.Fatal("EnsureDir 应成功")
	}
	Write("x/y/1.txt", "1")
	Write("x/y/2.txt", "2")
	list := ListDir("x/y")
	if len(list) != 2 {
		t.Fatalf("ListDir = %v", list)
	}
	if !IsDir("x/y") || !IsFile("x/y/1.txt") {
		t.Fatal("IsDir/IsFile 判断错")
	}
}

func TestCopyMoveRemove(t *testing.T) {
	newRoot(t)
	Write("src.txt", "abc")
	if !Copy("src.txt", "dst.txt") {
		t.Fatal("Copy 失败")
	}
	if Read("dst.txt") != "abc" {
		t.Fatal("复制内容不对")
	}
	if !Move("dst.txt", "moved.txt") {
		t.Fatal("Move 失败")
	}
	if Exists("dst.txt") || Read("moved.txt") != "abc" {
		t.Fatal("Move 后状态不对")
	}
	if !Remove("moved.txt") || Exists("moved.txt") {
		t.Fatal("Remove 失败")
	}
}

func TestNamesAndMd5(t *testing.T) {
	newRoot(t)
	if GetName("a/b/c.txt") != "c.txt" {
		t.Fatalf("GetName = %q", GetName("a/b/c.txt"))
	}
	if GetNameWithoutExtension("a/b/c.txt") != "c" {
		t.Fatalf("GetNameWithoutExtension = %q", GetNameWithoutExtension("a/b/c.txt"))
	}
	if GetExtension("a/b/c.txt") != "txt" {
		t.Fatalf("GetExtension = %q", GetExtension("a/b/c.txt"))
	}
	Write("m.txt", "abc")
	// md5("abc") = 900150983cd24fb0d6963f7d28e17f72
	if got := GetMd5("m.txt"); got != "900150983cd24fb0d6963f7d28e17f72" {
		t.Fatalf("GetMd5 = %q", got)
	}
}

func TestPlistRoundTrip(t *testing.T) {
	newRoot(t)
	data := map[string]interface{}{
		"name":  "agov",
		"count": "3",
		"ok":    "true",
	}
	if !WritePlist("p.plist", data) {
		t.Fatal("WritePlist 失败")
	}
	got := ReadPlist("p.plist")
	if got == nil {
		t.Fatal("ReadPlist 返回 nil")
	}
	if got["name"] != "agov" {
		t.Fatalf("name = %v", got["name"])
	}
	if v := GetPlistValue("p.plist", "name"); v != "agov" {
		t.Fatalf("GetPlistValue = %v", v)
	}
	if !SetPlistValue("p.plist", "name", "new") {
		t.Fatal("SetPlistValue 失败")
	}
	if v := GetPlistValue("p.plist", "name"); v != "new" {
		t.Fatalf("SetPlistValue 后 = %v", v)
	}
}

// 截断的二进制 plist 必须报解析失败，不能当成空配置。
func TestReadPlistRejectsTruncatedBinary(t *testing.T) {
	newRoot(t)
	WriteBytes("b.plist", []byte("bplist00\x00\x01\x02"))
	if got := ReadPlist("b.plist"); got != nil {
		t.Fatalf("截断的二进制 plist 应返回 nil，实际 %v", got)
	}
}

// 使用真实的二进制编码，验证日期、数据和嵌套结构没有被字符串化。
func TestBinaryPlistNestedValues(t *testing.T) {
	newRoot(t)
	date := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	original := map[string]any{"nested": map[string]any{"enabled": true}, "data": []byte{1, 2, 3}, "date": date, "items": []any{"a", uint64(7)}}
	raw, err := plist.Marshal(original, plist.BinaryFormat)
	if err != nil {
		t.Fatal(err)
	}
	WriteBytes("nested.plist", raw)
	got := ReadPlist("nested.plist")
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("二进制结构丢失: %#v", got)
	}
	if !SetPlistValue("nested.plist", "extra", false) {
		t.Fatal("更新失败")
	}
	got = ReadPlist("nested.plist")
	if got["extra"] != false || !reflect.DeepEqual(got["nested"], original["nested"]) || !bytes.Equal(got["data"].([]byte), []byte{1, 2, 3}) {
		t.Fatalf("更新破坏原值: %#v", got)
	}
}
func TestEmptyDirectoryAndRenameDestination(t *testing.T) {
	newRoot(t)
	EnsureDir("empty")
	if !IsEmptyDir("empty") || IsEmptyDir("missing") {
		t.Fatal("空目录判断错误")
	}
	Write("empty/file", "x")
	if IsEmptyDir("empty") {
		t.Fatal("有文件的目录被判为空")
	}
	Write("a.txt", "x")
	if !Rename("a.txt", "empty/new.txt") || Read("empty/new.txt") != "x" {
		t.Fatal("Rename 未按目标路径移动")
	}
}
