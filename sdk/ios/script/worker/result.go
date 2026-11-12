package worker

import (
	"fmt"
	"io"
	"os"
)

const maxResultBytes = 16 << 20

// 结果在子进程确认退出后读取；拒绝链接、替换和超大文件，避免把异常文件载入宿主。
func readWorkerResult(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > maxResultBytes {
		return nil, fmt.Errorf("工作进程结果不是有界常规文件")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, fmt.Errorf("工作进程结果读取前发生替换")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxResultBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxResultBytes {
		return nil, fmt.Errorf("工作进程结果超过 16 MiB")
	}
	return raw, nil
}
