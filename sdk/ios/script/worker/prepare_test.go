package worker

import (
	"bytes"
	"context"
	"debug/macho"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"autogov1/v2/sdk/ios/device"
)

// 构造只用于依赖解析的最小 Mach-O，不执行任何二进制，也不需要 Apple SDK。
func workerMachO(kind macho.Type, platform uint32, imports ...string) []byte {
	order := binary.LittleEndian
	commands := make([]byte, 24)
	order.PutUint32(commands, 0x32)
	order.PutUint32(commands[4:], 24)
	order.PutUint32(commands[8:], platform)
	for _, name := range imports {
		length := (24 + len(name) + 1 + 7) &^ 7
		command := make([]byte, length)
		order.PutUint32(command, 0xc)
		order.PutUint32(command[4:], uint32(length))
		order.PutUint32(command[8:], 24)
		copy(command[24:], name)
		commands = append(commands, command...)
	}
	header := make([]byte, 32)
	order.PutUint32(header, macho.Magic64)
	order.PutUint32(header[4:], uint32(macho.CpuArm64))
	order.PutUint32(header[12:], uint32(kind))
	order.PutUint32(header[16:], uint32(1+len(imports)))
	order.PutUint32(header[20:], uint32(len(commands)))
	return append(header, commands...)
}
func writeWorkerFixture(t *testing.T, filename string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, raw, 0644); err != nil {
		t.Fatal(err)
	}
}
func workerFixture(t *testing.T) (device.RuntimePaths, string) {
	t.Helper()
	root := t.TempDir()
	paths := device.RuntimePaths{Bundle: filepath.Join(root, "App"), Documents: filepath.Join(root, "Documents")}
	sdk := filepath.Join(paths.Bundle, "libagovios.dylib")
	writeWorkerFixture(t, filepath.Join(paths.Bundle, "Runtime", "app"), workerMachO(macho.TypeExec, 2, "/usr/lib/libSystem.B.dylib"))
	writeWorkerFixture(t, sdk, workerMachO(macho.TypeDylib, 2, "@rpath/libimgui.dylib", "@rpath/libopencv_imgproc.dylib"))
	writeWorkerFixture(t, filepath.Join(paths.Bundle, "Runtime", "libimgui.dylib"), workerMachO(macho.TypeDylib, 2))
	writeWorkerFixture(t, filepath.Join(paths.Bundle, "Runtime", "libopencv_imgproc.dylib"), workerMachO(macho.TypeDylib, 2, "@rpath/libopencv_core.dylib"))
	writeWorkerFixture(t, filepath.Join(paths.Bundle, "Runtime", "libopencv_core.dylib"), workerMachO(macho.TypeDylib, 2))
	return paths, sdk
}
func TestPrepareWorkerUsesPackagedRuntimeAndTransitiveLibraries(t *testing.T) {
	paths, sdk := workerFixture(t)
	// 同时覆盖后续 dlopen 引擎的间接依赖，不能只收集 SDK 的直接链接。
	writeWorkerFixture(t, filepath.Join(paths.Bundle, "Runtime", "libyolo.dylib"), workerMachO(macho.TypeDylib, 2, "@rpath/libncnn.dylib"))
	writeWorkerFixture(t, filepath.Join(paths.Bundle, "Runtime", "libncnn.dylib"), workerMachO(macho.TypeDylib, 2))
	app, err := prepareWorkerBinary(context.Background(), paths, sdk)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app", "libimgui.dylib", "libopencv_imgproc.dylib", "libopencv_core.dylib", "libyolo.dylib", "libncnn.dylib"} {
		source := filepath.Join(paths.Bundle, "Runtime", name)
		want, _ := os.ReadFile(source)
		got, err := os.ReadFile(filepath.Join(filepath.Dir(app), name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("未准备完整依赖 %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(app), "libagovios.dylib")); !os.IsNotExist(err) {
		t.Fatal("不应另复制一份 SDK")
	}
}
func TestPrepareWorkerAcceptsSimulatorRootLibrary(t *testing.T) {
	paths, sdk := workerFixture(t)
	source := filepath.Join(paths.Bundle, "Runtime", "libimgui.dylib")
	raw, _ := os.ReadFile(source)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	writeWorkerFixture(t, filepath.Join(paths.Bundle, "libimgui.dylib"), raw)
	if _, err := prepareWorkerBinary(context.Background(), paths, sdk); err != nil {
		t.Fatal(err)
	}
}
func TestSameSizeNewLibraryGetsNewRuntimeWithoutOverwritingOld(t *testing.T) {
	paths, sdk := workerFixture(t)
	first, err := prepareWorkerBinary(context.Background(), paths, sdk)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(paths.Bundle, "Runtime", "libimgui.dylib")
	old, _ := os.ReadFile(source)
	updated := append([]byte(nil), old...)
	updated[24] ^= 1 // 改 flags，长度与有效加载命令均保持不变。
	writeWorkerFixture(t, source, updated)
	second, err := prepareWorkerBinary(context.Background(), paths, sdk)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("同大小的新版本被当作旧缓存")
	}
	previous, _ := os.ReadFile(filepath.Join(filepath.Dir(first), "libimgui.dylib"))
	current, _ := os.ReadFile(filepath.Join(filepath.Dir(second), "libimgui.dylib"))
	if !bytes.Equal(previous, old) || !bytes.Equal(current, updated) {
		t.Fatal("旧运行目录被覆盖或新版本未生效")
	}
}
func TestPrepareWorkerRepairsDeletedAndSameSizeCorruptCache(t *testing.T) {
	paths, sdk := workerFixture(t)
	app, err := prepareWorkerBinary(context.Background(), paths, sdk)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(filepath.Dir(app), "libimgui.dylib")
	raw, _ := os.ReadFile(cache)
	corrupt := bytes.Repeat([]byte{0}, len(raw))
	if err := os.WriteFile(cache, corrupt, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = prepareWorkerBinary(context.Background(), paths, sdk); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cache)
	if !bytes.Equal(got, raw) {
		t.Fatal("同大小的损坏缓存没有修复")
	}
	if err = os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	if _, err = prepareWorkerBinary(context.Background(), paths, sdk); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(cache); err != nil {
		t.Fatal("删除的库未重新补齐", err)
	}
}
func TestPrepareWorkerMissingDependencyFailsBeforeLaunch(t *testing.T) {
	paths, sdk := workerFixture(t)
	if err := os.Remove(filepath.Join(paths.Bundle, "Runtime", "libimgui.dylib")); err != nil {
		t.Fatal(err)
	}
	app, err := prepareWorkerBinary(context.Background(), paths, sdk)
	if app != "" || !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "libimgui.dylib") {
		t.Fatalf("缺依赖被忽略: %q %v", app, err)
	}
}
func TestPrepareWorkerCopyFailureIsNotIgnored(t *testing.T) {
	paths, sdk := workerFixture(t)
	app, err := prepareWorkerBinary(context.Background(), paths, sdk)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(filepath.Dir(app), "libimgui.dylib")
	if err = os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(destination, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = prepareWorkerBinary(context.Background(), paths, sdk); err == nil || !strings.Contains(err.Error(), "libimgui.dylib") {
		t.Fatalf("复制失败未阻止启动: %v", err)
	}
}
func TestPrepareWorkerRejectsWrongPlatformAndCanceledContext(t *testing.T) {
	paths, sdk := workerFixture(t)
	writeWorkerFixture(t, filepath.Join(paths.Bundle, "Runtime", "libimgui.dylib"), workerMachO(macho.TypeDylib, 7))
	if _, err := prepareWorkerBinary(context.Background(), paths, sdk); err == nil {
		t.Fatal("真机包错误接受模拟器依赖")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := prepareWorkerBinary(ctx, paths, sdk); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消后仍继续准备: %v", err)
	}
}

func TestAtomicInstallRejectsChangedSourceWithoutDamagingExistingFile(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	destination := filepath.Join(dir, "cached")
	writeWorkerFixture(t, source, []byte("release"))
	writeWorkerFixture(t, destination, []byte("current"))
	digest, size, err := hashWorkerFile(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	writeWorkerFixture(t, source, []byte("changed"))
	err = installWorkerFile(context.Background(), workerFile{source: source, name: "cached", digest: digest, size: size}, destination)
	if err == nil {
		t.Fatal("读取期间改变的源文件不应被安装")
	}
	raw, _ := os.ReadFile(destination)
	if string(raw) != "current" {
		t.Fatal("旧缓存被部分写入覆盖")
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".prepare-*"))
	if len(leftovers) != 0 {
		t.Fatal("失败后留下临时文件", leftovers)
	}
}

func TestPrepareWorkerRejectsRedirectedCacheDirectory(t *testing.T) {
	paths, sdk := workerFixture(t)
	if err := os.MkdirAll(paths.Documents, 0755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(paths.Documents, "Worker")); err != nil {
		t.Skip("当前环境不能创建软链接", err)
	}
	if _, err := prepareWorkerBinary(context.Background(), paths, sdk); err == nil {
		t.Fatal("工作目录软链接不应被跟随写入")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("发生了缓存目录之外的写入", err)
	}
}
