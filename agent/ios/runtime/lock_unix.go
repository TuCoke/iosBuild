//go:build !windows

package runtime

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

// 内核文件锁随进程退出释放，不删除锁文件，避免旧 inode 与新 inode 同时被持有。
func lockInstance(dir string) (func(), error) {
	fd, e := unix.Open(filepath.Join(dir, "agent.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), "agent.lock")
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("该 iOS Agent 数据目录已被另一进程持有")
	}
	return func() { _ = f.Close() }, nil
}
