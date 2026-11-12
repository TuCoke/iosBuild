package tunnel

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrepareBinaryRepairsPermissionWithoutChangingBundle(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "frpc-ios")
	image := []byte("signed executable bytes")
	if err := os.WriteFile(source, image, 0644); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "Tunnel")
	prepared, err := prepareBinary(context.Background(), source, directory)
	if err != nil {
		t.Fatal(err)
	}
	assertImage := func(path string) {
		t.Helper()
		actual, err := os.ReadFile(path)
		if err != nil || string(actual) != string(image) {
			t.Fatalf("文件内容被改变: %s, %v", path, err)
		}
	}
	assertImage(source)
	assertImage(prepared)
	if runtime.GOOS != "windows" {
		original, _ := os.Stat(source)
		copied, _ := os.Stat(prepared)
		if original.Mode().Perm() != 0644 || copied.Mode().Perm() != 0755 {
			t.Fatalf("权限异常: source=%v copy=%v", original.Mode(), copied.Mode())
		}
	}
	// 已缓存文件也可能丢失执行位，重复启动必须能够修复。
	if err := os.Chmod(prepared, 0644); err != nil {
		t.Fatal(err)
	}
	again, err := prepareBinary(context.Background(), source, directory)
	if err != nil || again != prepared {
		t.Fatalf("复用缓存失败: %q %v", again, err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(again)
		if info.Mode().Perm() != 0755 {
			t.Fatalf("未修复执行位: %v", info.Mode())
		}
	}
	// 缓存内容损坏时重新安装，不能执行未经核对的内容。
	if err := os.WriteFile(prepared, []byte("corrupt"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareBinary(context.Background(), source, directory); err != nil {
		t.Fatal(err)
	}
	assertImage(prepared)
	if err := os.WriteFile(source, []byte("new signed image"), 0644); err != nil {
		t.Fatal(err)
	}
	updated, err := prepareBinary(context.Background(), source, directory)
	if err != nil || updated == prepared {
		t.Fatalf("升级覆盖了旧映像: %q %v", updated, err)
	}
	assertImage(prepared)
}

func TestPrepareBinaryRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := prepareBinary(ctx, "missing", t.TempDir()); err != context.Canceled {
		t.Fatalf("取消未生效: %v", err)
	}
}
