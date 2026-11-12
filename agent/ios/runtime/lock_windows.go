//go:build windows

package runtime

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

// Windows 仅用于协议联调测试；设备端使用 Unix 内核文件锁。
func lockInstance(dir string) (func(), error) {
	f, e := os.OpenFile(filepath.Join(dir, "agent.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	var o windows.Overlapped
	if e = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &o); e != nil {
		f.Close()
		return nil, fmt.Errorf("该 iOS Agent 数据目录已被另一进程持有")
	}
	return func() { _ = f.Close() }, nil
}
