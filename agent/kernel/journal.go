// Package kernel 提供不依赖 Android 原生库的设备执行日志。
package kernel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"autogov1/v2/protocol"
)

type entry struct {
	Digest  string                  `json:"digest"`
	Receipt protocol.CommandReceipt `json:"receipt"`
}

// Journal 不保存命令正文；文件只含参数摘要和回执，目录必须属于 Agent 私有数据。
// 首次预约使用排他创建；日志损坏或落盘失败时拒绝执行，不能退回内存去重。
type Journal struct {
	mu     sync.Mutex
	dir    string
	active map[string]entry
	count  int
	fence  fenceState
}

func Open(dir string) (*Journal, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	j := &Journal{dir: dir, active: map[string]entry{}}
	if err := j.loadFence(); err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.IsDir() || filepath.Ext(item.Name()) != ".json" {
			continue
		}
		j.count++
	}
	return j, nil
}

func (j *Journal) path(id string) string {
	digest := sha256.Sum256([]byte(id))
	return filepath.Join(j.dir, hex.EncodeToString(digest[:])+".json")
}

func (j *Journal) read(id string) (entry, error) {
	if e, ok := j.active[id]; ok {
		return e, nil
	}
	data, err := os.ReadFile(j.path(id))
	if err != nil {
		return entry{}, err
	}
	var e entry
	if json.Unmarshal(data, &e) != nil || e.Receipt.ID != id {
		return entry{}, fmt.Errorf("设备执行记录损坏")
	}
	// 不属于本进程的非终态可能已执行，重启后不能重播。
	if e.Receipt.State == "queued" || e.Receipt.State == "running" {
		e.Receipt.State = "unknown"
		e.Receipt.OK = false
		e.Receipt.Msg = "原执行进程已退出，需核对现场"
	}
	return e, nil
}

func (j *Journal) persist(e entry, exclusive bool) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(data) > 256<<10 {
		return fmt.Errorf("设备回执超过持久化上限")
	}
	path := j.path(e.Receipt.ID)
	if exclusive {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		return syncDirectory(j.dir)
	}
	f, err := os.CreateTemp(j.dir, "receipt-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return syncDirectory(j.dir)
}

// Reserve 将执行身份和内容绑定；同 ID 异参数拒绝，已有记录只查询不再次安排。
func (j *Journal) Reserve(c protocol.Cmd) (protocol.CommandReceipt, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	copy := c
	copy.SessionID = ""
	copy.DeadlineAt = 0
	copy.ID = ""
	copy.Type = ""
	copy.Fence = 0
	data, err := json.Marshal(copy)
	if err != nil {
		return protocol.CommandReceipt{}, false, err
	}
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	previous, err := j.read(c.ID)
	if err == nil {
		if previous.Digest != digest {
			return protocol.CommandReceipt{}, false, fmt.Errorf("请求标识已用于其他参数")
		}
		return previous.Receipt, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return protocol.CommandReceipt{}, false, err
	}
	if j.count >= 65536 {
		return protocol.CommandReceipt{}, false, fmt.Errorf("设备执行记录已达上限，需清理已归档记录")
	}
	e := entry{Digest: digest, Receipt: protocol.CommandReceipt{ID: c.ID, State: "queued", UpdatedAt: time.Now().UnixMilli()}}
	if err = j.persist(e, true); err != nil {
		return protocol.CommandReceipt{}, false, err
	}
	j.active[c.ID] = e
	j.count++
	return e.Receipt, true, nil
}

func (j *Journal) Start(id string) (bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	e, ok := j.active[id]
	if !ok || e.Receipt.State != "queued" {
		return false, nil
	}
	e.Receipt.State = "running"
	e.Receipt.UpdatedAt = time.Now().UnixMilli()
	if err := j.persist(e, false); err != nil {
		return false, err
	}
	j.active[id] = e
	return true, nil
}

// Complete 先保存后通知云端；存储故障只能返回未知，不能伪造执行成功。
func (j *Journal) Complete(id string, state string, ok bool, msg string, data any) (protocol.CommandReceipt, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	e, exists := j.active[id]
	if !exists {
		return protocol.CommandReceipt{}, fmt.Errorf("设备执行记录不存在")
	}
	if state != "returned" && state != "unknown" {
		return protocol.CommandReceipt{}, fmt.Errorf("设备回执状态无效")
	}
	e.Receipt = protocol.CommandReceipt{ID: id, State: state, OK: ok, Msg: msg, Data: data, UpdatedAt: time.Now().UnixMilli()}
	if err := j.persist(e, false); err != nil {
		return protocol.CommandReceipt{}, err
	}
	delete(j.active, id)
	return e.Receipt, nil
}

// Cancel 仅撤销尚未开始的命令；运行中的原生动作不能被假装撤回。
func (j *Journal) Cancel(id string) (protocol.CommandReceipt, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	e, err := j.read(id)
	if err != nil {
		return protocol.CommandReceipt{}, err
	}
	if e.Receipt.State == "queued" {
		e.Receipt.State = "canceled"
		e.Receipt.Msg = "命令在执行前取消"
		e.Receipt.UpdatedAt = time.Now().UnixMilli()
		if err = j.persist(e, false); err != nil {
			return protocol.CommandReceipt{}, err
		}
		delete(j.active, id)
	}
	return e.Receipt, nil
}

func (j *Journal) Result(id string) (protocol.CommandReceipt, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	e, err := j.read(id)
	return e.Receipt, err
}

// Prune 只删除指定时间前的已返回或执行前取消记录；未知和损坏记录需要人工核对。
func (j *Journal) Prune(before time.Time) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	items, err := os.ReadDir(j.dir)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.IsDir() || filepath.Ext(item.Name()) != ".json" {
			continue
		}
		path := filepath.Join(j.dir, item.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var e entry
		if json.Unmarshal(data, &e) != nil || e.Receipt.UpdatedAt >= before.UnixMilli() {
			continue
		}
		if _, active := j.active[e.Receipt.ID]; active {
			continue
		}
		if e.Receipt.State != "returned" && e.Receipt.State != "canceled" {
			continue
		}
		if err = os.Remove(path); err != nil {
			return err
		}
		j.count--
	}
	return nil
}
