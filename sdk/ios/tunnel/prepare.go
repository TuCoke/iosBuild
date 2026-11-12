package tunnel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// prepareBinary 将包内签名文件原样复制到可写目录，修复安装后丢失执行位的问题。
// 不修改 App 包，也不改写 Mach-O 内容；最终是否允许执行仍以系统返回为准。
func prepareBinary(ctx context.Context, source, directory string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 64<<20 {
		return "", fmt.Errorf("frpc 文件不是有效的常规文件: %s", source)
	}
	image, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(image))
	// 按内容分目录，升级时不覆盖仍可能运行的旧进程映像。
	for _, part := range []string{"", "Bin", filepath.Join("Bin", digest)} {
		path := filepath.Join(directory, part)
		if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			return "", err
		}
		entry, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if !entry.IsDir() || entry.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("frpc 缓存目录不可使用符号链接: %s", path)
		}
		if err := os.Chmod(path, 0700); err != nil {
			return "", fmt.Errorf("修复 frpc 运行目录权限失败: %w", err)
		}
	}
	destination := filepath.Join(directory, "Bin", digest, "frpc-ios")
	if entry, err := os.Lstat(destination); err == nil {
		if !entry.Mode().IsRegular() {
			return "", fmt.Errorf("frpc 缓存不是常规文件: %s", destination)
		}
		if entry.Size() == int64(len(image)) {
			cached, err := os.ReadFile(destination)
			if err == nil && bytes.Equal(cached, image) {
				return destination, os.Chmod(destination, 0755)
			}
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".frpc-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(image)
	if writeErr == nil {
		writeErr = file.Chmod(0755)
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.Rename(file.Name(), destination); err != nil {
		return "", err
	}
	return destination, nil
}
