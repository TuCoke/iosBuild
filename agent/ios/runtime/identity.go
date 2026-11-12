package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 首次沿用联调桥的 ID，之后以手机私有目录持久身份为准，在私有目录保留时避免重装导致重复设备。
func deviceIdentity(dir, vendor string) (string, error) {
	path := filepath.Join(dir, "device-id")
	if b, e := os.ReadFile(path); e == nil {
		v := strings.TrimSpace(string(b))
		if strings.HasPrefix(v, "ios-") && len(v) == 20 {
			if _, e = hex.DecodeString(v[4:]); e == nil {
				return v, nil
			}
		}
		return "", fmt.Errorf("iOS Agent 身份文件损坏，不能自动生成新身份")
	} else if !os.IsNotExist(e) {
		return "", e
	}
	sum := sha256.Sum256([]byte(vendor))
	id := "ios-" + hex.EncodeToString(sum[:8])
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", e
	}
	if _, e = f.WriteString(id); e != nil {
		f.Close()
		return "", e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return "", e
	}
	if e = f.Close(); e != nil {
		return "", e
	}
	return id, nil
}
