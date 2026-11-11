// Package resources 检查制品中实际交付的原生运行库；清单存在不代表模型已加载或识别效果通过。
package resources

import (
	"context"
	"crypto/sha256"
	"debug/macho"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"autogov1/v2/sdk/ios/device"
)

type Entry struct {
	Path       string `json:"path"`
	BundlePath string `json:"bundlePath"`
	Kind       string `json:"kind"`
	Platform   string `json:"platform"`
	Group      string `json:"group"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
}
type Item struct {
	Entry
	ActualBytes int64  `json:"actualBytes"`
	Exists      bool   `json:"exists"`
	Verified    bool   `json:"verified"`
	Error       string `json:"error,omitempty"`
}
type Report struct {
	Ready    bool   `json:"ready"`
	Platform string `json:"platform"`
	Items    []Item `json:"items"`
}

func filePlatform(file *os.File) (string, error) {
	image, err := macho.NewFile(file)
	if err != nil {
		return "", err
	}
	defer image.Close()
	if image.Cpu != macho.CpuArm64 || image.Type != macho.TypeDylib {
		return "", fmt.Errorf("不是 arm64 动态库")
	}
	legacy := false
	for _, load := range image.Loads {
		raw := load.Raw()
		if len(raw) < 8 {
			continue
		}
		command := image.ByteOrder.Uint32(raw)
		if command == 0x32 {
			if len(raw) < 24 {
				return "", fmt.Errorf("平台载荷截断")
			}
			switch image.ByteOrder.Uint32(raw[8:12]) {
			case 2:
				return "ios", nil
			case 7:
				return "ios-simulator", nil
			default:
				return "", fmt.Errorf("不是 iOS 平台")
			}
		}
		if command == 0x25 {
			legacy = true
		}
	}
	if legacy {
		return "ios", nil
	}
	return "", fmt.Errorf("没有平台标记")
}

// Inspect 默认逐项检查普通文件、大小上限和目标平台，verifyHash 为 true 时再读取摘要。
// codesign/install_name_tool 会改变 Mach-O 文件摘要；运行期报告此差异，不能冒充原始资源完全一致。
// 发布前的原始资源锁检查由 tools/native_resources.py 执行。
func Inspect(ctx context.Context, verifyHash bool) (Report, error) {
	paths, err := device.Paths(ctx)
	if err != nil {
		return Report{}, err
	}
	report := Report{Ready: false, Platform: paths.Platform, Items: []Item{}}
	f, err := os.Open(filepath.Join(paths.Bundle, "native-resources.lock.json"))
	if err != nil {
		return report, fmt.Errorf("原生资源清单缺失: %w", err)
	}
	defer f.Close()
	var manifest struct {
		Version   int     `json:"version"`
		Resources []Entry `json:"resources"`
	}
	if err = json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&manifest); err != nil {
		return report, err
	}
	if manifest.Version != 1 || len(manifest.Resources) == 0 {
		return report, fmt.Errorf("原生资源清单无效")
	}
	report.Ready = true
	for _, entry := range manifest.Resources {
		if err = ctx.Err(); err != nil {
			return report, err
		}
		item := Item{Entry: entry}
		rel := filepath.FromSlash(entry.BundlePath)
		if rel == "" || filepath.IsAbs(rel) || filepath.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			item.Error = "包内路径无效"
		} else if entry.Platform != paths.Platform {
			item.Error = "资源目标平台与宿主不符"
		} else {
			file, openErr := os.Open(filepath.Join(paths.Bundle, rel))
			if openErr != nil {
				item.Error = openErr.Error()
			} else {
				info, statErr := file.Stat()
				item.Exists = statErr == nil && info.Mode().IsRegular()
				if item.Exists {
					item.ActualBytes = info.Size()
				}
				if !item.Exists {
					item.Error = "资源不是普通文件"
				} else if info.Size() <= 0 || info.Size() > 256<<20 {
					item.Error = "资源大小无效"
				} else if entry.Kind == "macho" {
					platform, platformErr := filePlatform(file)
					if platformErr != nil {
						item.Error = platformErr.Error()
					} else if platform != paths.Platform {
						item.Error = "文件实际平台与宿主不符"
					}
				}
				if item.Error == "" && verifyHash {
					hash := sha256.New()
					_, hashErr := io.Copy(hash, io.LimitReader(file, 256<<20+1))
					if hashErr != nil {
						item.Error = hashErr.Error()
					} else if hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
						item.Error = "资源摘要与发布前锁不同（签名或重定位也会改变文件）"
					} else {
						item.Verified = true
					}
				}
				file.Close()
			}
		}
		if item.Error != "" {
			report.Ready = false
		}
		report.Items = append(report.Items, item)
	}
	return report, nil
}
