package runtime

import (
	"autogov1/v2/agent/kernel"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// 云控地址属于执行身份的一部分，不能让另一台服务端继承旧栅栏或旧回执。
// 每个私有数据目录仍对应唯一手机身份；不移动、不清空旧执行日志。
func openCloudJournal(cfg Config) (*kernel.Journal, error) {
	entries, err := os.ReadDir(filepath.Join(cfg.DataDir, "journal"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if len(entries) > 0 {
		return nil, fmt.Errorf("发现未绑定云控地址的旧 Agent 执行日志，请先核对迁移归属；不能自动丢弃旧栅栏")
	}
	sum := sha256.Sum256([]byte(cfg.Server))
	return kernel.Open(filepath.Join(cfg.DataDir, "journals", hex.EncodeToString(sum[:])))
}
