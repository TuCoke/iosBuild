package worker

import (
	"context"
	"crypto/sha256"
	"debug/macho"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"autogov1/v2/sdk/ios/device"
)

const maxWorkerFileBytes = 256 << 20

type workerImport struct {
	name string
	weak bool
}
type workerImage struct {
	cpu      macho.Cpu
	platform uint32
	kind     macho.Type
	imports  []workerImport
}
type workerFile struct {
	source, name, digest string
	size                 int64
}

// prepareWorkerBinary 不修改只读 App 包。SDK、执行体和依赖的摘要共同确定缓存目录，
// 新版本不会覆盖另一工作进程可能仍在使用的文件；同一版本的文件损坏也会重新补齐。
func prepareWorkerBinary(ctx context.Context, paths device.RuntimePaths, sdkLibrary string) (string, error) {
	if ctx == nil || !filepath.IsAbs(paths.Bundle) || !filepath.IsAbs(paths.Documents) || !filepath.IsAbs(sdkLibrary) {
		return "", fmt.Errorf("工作进程缺少有效的 context、Bundle、Documents 或 SDK 绝对路径")
	}
	sdk, err := readWorkerImage(sdkLibrary)
	if err != nil {
		return "", fmt.Errorf("读取 SDK 依赖失败: %w", err)
	}
	if sdk.kind != macho.TypeDylib {
		return "", fmt.Errorf("SDK 不是 Mach-O 动态库: %s", sdkLibrary)
	}
	sdkHash, _, err := hashWorkerFile(ctx, sdkLibrary)
	if err != nil {
		return "", err
	}
	files := map[string]workerFile{}
	visited := map[string]bool{}
	var collect func(string, bool) error
	collect = func(source string, executable bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if visited[source] {
			return nil
		}
		visited[source] = true
		image, err := readWorkerImage(source)
		if err != nil {
			return err
		}
		expected := macho.TypeDylib
		if executable {
			expected = macho.TypeExec
		}
		if image.kind != expected || image.cpu != sdk.cpu || image.platform != sdk.platform {
			return fmt.Errorf("工作进程文件类型、CPU 或平台不匹配: %s", source)
		}
		digest, size, err := hashWorkerFile(ctx, source)
		if err != nil {
			return err
		}
		name := filepath.Base(source)
		if old, ok := files[name]; ok && old.digest != digest {
			return fmt.Errorf("工作进程依赖同名但内容冲突: %s 与 %s", old.source, source)
		}
		files[name] = workerFile{source: source, name: name, digest: digest, size: size}
		if len(files) > 128 {
			return fmt.Errorf("工作进程依赖超过 128 个文件")
		}
		for _, dependency := range image.imports {
			if systemWorkerLibrary(dependency.name) {
				continue
			}
			resolved, err := resolveWorkerLibrary(source, dependency.name, paths.Bundle)
			if err != nil {
				if dependency.weak && os.IsNotExist(err) {
					continue
				}
				return err
			}
			if err = collect(resolved, false); err != nil {
				return err
			}
		}
		return nil
	}
	if err = collect(filepath.Join(paths.Bundle, "Runtime", "app"), true); err != nil {
		return "", fmt.Errorf("准备执行体失败: %w", err)
	}
	// SDK 本身继续使用 ActiveLibrary 的原路径，只搬它的实际依赖。
	for _, dependency := range sdk.imports {
		if systemWorkerLibrary(dependency.name) {
			continue
		}
		resolved, resolveErr := resolveWorkerLibrary(sdkLibrary, dependency.name, paths.Bundle)
		if resolveErr != nil {
			if dependency.weak && os.IsNotExist(resolveErr) {
				continue
			}
			return "", resolveErr
		}
		if err = collect(resolved, false); err != nil {
			return "", err
		}
	}
	// 识别引擎是后续按绝对路径 dlopen 的，但其二级依赖仍可能使用 @rpath。
	// 一并纳入包内 Runtime 库，不能误认为“绝对路径加载”就没有间接依赖。
	entries, err := os.ReadDir(filepath.Join(paths.Bundle, "Runtime"))
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".dylib") {
			if err = collect(filepath.Join(paths.Bundle, "Runtime", entry.Name()), false); err != nil {
				return "", err
			}
		}
	}
	ordered := make([]workerFile, 0, len(files))
	for _, file := range files {
		ordered = append(ordered, file)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].name < ordered[j].name })
	fingerprint := sha256.New()
	fmt.Fprintf(fingerprint, "worker-runtime-v1\x00%s\x00", sdkHash)
	for _, file := range ordered {
		fmt.Fprintf(fingerprint, "%s\x00%s\x00", file.name, file.digest)
	}
	dir := filepath.Join(paths.Documents, "Worker", hex.EncodeToString(fingerprint.Sum(nil)))
	for _, directory := range []string{filepath.Dir(dir), dir} {
		if err = os.MkdirAll(directory, 0700); err != nil {
			return "", fmt.Errorf("创建工作进程运行目录失败: %w", err)
		}
		info, statErr := os.Lstat(directory)
		if statErr != nil {
			return "", statErr
		}
		if !info.IsDir() {
			return "", fmt.Errorf("工作进程缓存目录不能是软链接或普通文件: %s", directory)
		}
	}
	for _, file := range ordered {
		if err = installWorkerFile(ctx, file, filepath.Join(dir, file.name)); err != nil {
			return "", fmt.Errorf("准备工作进程文件 %s 失败: %w", file.name, err)
		}
	}
	return filepath.Join(dir, "app"), nil
}

func systemWorkerLibrary(name string) bool {
	return strings.HasPrefix(name, "/System/Library/") || strings.HasPrefix(name, "/usr/lib/")
}

// 本项目发布的第三方依赖使用平铺 dylib。未知目录布局直接报错，不能静默拼错路径。
func resolveWorkerLibrary(owner, name, bundle string) (string, error) {
	var relative string
	switch {
	case strings.HasPrefix(name, "@rpath/"):
		relative = strings.TrimPrefix(name, "@rpath/")
	case strings.HasPrefix(name, "@loader_path/"):
		relative = strings.TrimPrefix(name, "@loader_path/")
	default:
		return "", fmt.Errorf("工作进程依赖路径暂不支持，请检查发布产物: %s（引用者 %s）", name, owner)
	}
	if relative == "" || relative == "." || relative == ".." || strings.ContainsAny(relative, "/\\") || !strings.HasSuffix(relative, ".dylib") {
		return "", fmt.Errorf("工作进程依赖不是受支持的平铺 dylib: %s", name)
	}
	candidates := []string{filepath.Join(bundle, "Runtime", relative), filepath.Join(filepath.Dir(owner), relative), filepath.Join(bundle, relative), filepath.Join(bundle, "Frameworks", relative)}
	if strings.HasPrefix(name, "@loader_path/") {
		candidates = candidates[1:2]
	}
	for _, candidate := range candidates {
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("读取依赖 %s 失败: %w", candidate, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("工作进程依赖不是普通文件: %s", candidate)
		}
		return candidate, nil
	}
	return "", fmt.Errorf("缺少工作进程依赖 %s（引用者 %s；已检查 Runtime、引用目录、App 根目录与 Frameworks）: %w", name, owner, os.ErrNotExist)
}

func readWorkerImage(filename string) (workerImage, error) {
	// 只读取 Mach-O 头和加载命令，不为查依赖而展开大型 Go SDK 的完整符号表。
	file, err := os.Open(filename)
	if err != nil {
		return workerImage{}, err
	}
	defer file.Close()
	header := make([]byte, 32)
	if _, err = io.ReadFull(file, header); err != nil {
		return workerImage{}, fmt.Errorf("读取 Mach-O 头 %s 失败: %w", filename, err)
	}
	var order binary.ByteOrder = binary.LittleEndian
	if order.Uint32(header) != macho.Magic64 {
		order = binary.BigEndian
		if order.Uint32(header) != macho.Magic64 {
			return workerImage{}, fmt.Errorf("工作进程文件不是受支持的薄 64 位 Mach-O: %s", filename)
		}
	}
	count, size := order.Uint32(header[16:20]), order.Uint32(header[20:24])
	if count == 0 || count > 65536 || size > 16<<20 || uint64(count)*8 > uint64(size) {
		return workerImage{}, fmt.Errorf("Mach-O 加载命令数量或大小无效: %s", filename)
	}
	commands := make([]byte, int(size))
	if _, err = io.ReadFull(file, commands); err != nil {
		return workerImage{}, fmt.Errorf("读取加载命令 %s 失败: %w", filename, err)
	}
	out := workerImage{cpu: macho.Cpu(order.Uint32(header[4:8])), kind: macho.Type(order.Uint32(header[12:16]))}
	offset := 0
	for i := uint32(0); i < count; i++ {
		if len(commands)-offset < 8 {
			return out, fmt.Errorf("Mach-O 加载命令截断: %s", filename)
		}
		length := order.Uint32(commands[offset+4 : offset+8])
		if length < 8 || uint64(length) > uint64(len(commands)-offset) {
			return out, fmt.Errorf("Mach-O 加载命令越界: %s", filename)
		}
		raw := commands[offset : offset+int(length)]
		offset += int(length)
		command := order.Uint32(raw) & 0x7fffffff
		switch command {
		case 0x32:
			if len(raw) < 24 {
				return out, fmt.Errorf("Mach-O 平台信息截断: %s", filename)
			}
			out.platform = order.Uint32(raw[8:12])
		case 0x25:
			if out.platform == 0 {
				out.platform = 2
				if out.cpu == macho.CpuAmd64 {
					out.platform = 7
				}
			}
		case 0xc, 0x18, 0x1f, 0x20, 0x23:
			if len(raw) < 24 {
				return out, fmt.Errorf("Mach-O 依赖信息截断: %s", filename)
			}
			nameOffset := order.Uint32(raw[8:12])
			if nameOffset < 24 || int(nameOffset) >= len(raw) {
				return out, fmt.Errorf("Mach-O 依赖名称越界: %s", filename)
			}
			name := strings.SplitN(string(raw[nameOffset:]), "\x00", 2)[0]
			out.imports = append(out.imports, workerImport{name: name, weak: command == 0x18})
		}
	}
	if out.platform != 2 && out.platform != 7 {
		return out, fmt.Errorf("工作进程文件不是可识别的 iOS/模拟器平台: %s", filename)
	}
	return out, nil
}

type workerContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r workerContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func hashWorkerFile(ctx context.Context, filename string) (string, int64, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxWorkerFileBytes {
		return "", 0, fmt.Errorf("工作进程文件类型或大小无效: %s", filename)
	}
	hash := sha256.New()
	count, err := io.Copy(hash, workerContextReader{ctx: ctx, reader: io.LimitReader(file, maxWorkerFileBytes+1)})
	if err != nil {
		return "", 0, err
	}
	if count != info.Size() {
		return "", 0, fmt.Errorf("工作进程文件在读取期间发生变化: %s", filename)
	}
	return hex.EncodeToString(hash.Sum(nil)), count, nil
}

// 原子替换避免截断正在执行/映射的文件；比对 SHA256，不能以长度相同判断版本相同。
func installWorkerFile(ctx context.Context, source workerFile, destination string) error {
	if info, err := os.Lstat(destination); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("工作进程缓存不是普通文件: %s", destination)
		}
		if info.Size() == source.size {
			digest, _, err := hashWorkerFile(ctx, destination)
			if err != nil {
				return err
			}
			if digest == source.digest {
				if err = os.Chmod(destination, 0755); err != nil {
					return fmt.Errorf("设置执行权限失败 %s: %w", destination, err)
				}
				return nil
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	input, err := os.Open(source.source)
	if err != nil {
		return err
	}
	defer input.Close()
	temp, err := os.CreateTemp(filepath.Dir(destination), ".prepare-*")
	if err != nil {
		return fmt.Errorf("准备工作进程文件 %s 失败: %w", destination, err)
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	hash := sha256.New()
	count, err := io.Copy(io.MultiWriter(temp, hash), workerContextReader{ctx: ctx, reader: io.LimitReader(input, maxWorkerFileBytes+1)})
	if err != nil {
		return err
	}
	if count != source.size || hex.EncodeToString(hash.Sum(nil)) != source.digest {
		return fmt.Errorf("复制期间源文件发生变化: %s", source.source)
	}
	if err = temp.Chmod(0755); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(temp.Name(), destination); err != nil {
		return fmt.Errorf("安装工作进程文件 %s 失败: %w", destination, err)
	}
	return nil
}
