//go:build !windows

package kernel

import "os"

// syncDirectory 保证预约及回执的目录变更在动作开始前落盘。
func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
