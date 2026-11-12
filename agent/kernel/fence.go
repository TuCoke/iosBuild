package kernel

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type fenceState struct {
	Version   int64  `json:"version"`
	RequestID string `json:"requestId"`
}

func (j *Journal) loadFence() error {
	body, err := os.ReadFile(filepath.Join(j.dir, "write-fence.state"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if json.Unmarshal(body, &j.fence) != nil || j.fence.Version < 1 || j.fence.RequestID == "" {
		return fmt.Errorf("设备写入栅栏记录损坏")
	}
	return nil
}

// AcceptFence 在动作开始之前持久推进高水位；旧执行者的迟到命令不能覆盖新写入。
// 已经进入原生函数的动作不能强杀，仍由未知隔离和人工现场核对处理。
func (j *Journal) AcceptFence(version int64, id string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if version < 1 || id == "" || version < j.fence.Version || (version == j.fence.Version && id != j.fence.RequestID) {
		return fmt.Errorf("设备写入栅栏已过期")
	}
	if version == j.fence.Version {
		return nil
	}
	value := fenceState{version, id}
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(j.dir, "fence-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(body)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(name, filepath.Join(j.dir, "write-fence.state")); err != nil {
		return err
	}
	j.fence = value
	if err := syncDirectory(j.dir); err != nil {
		return err
	}
	return nil
}
