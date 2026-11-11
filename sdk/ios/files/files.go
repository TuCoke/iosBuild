// Package files 对齐 AutoGo iOS 的文件系统 API（26 个函数，同名同参同返回）。
//
// 【为什么这一层用纯 Go 写】iOS 沙盒里的读写用 os 就够，不必过原生桥：
// 过桥会把每次 Read 变成一次 JSON 往返，几百次读盘的重活会被序列化开销拖死。
// 只有「应用沙盒根目录」这一个知识是平台相关的，放在 rootDir 里可替换。
//
// 【失败返回零值】AutoGo 的约定是读不到就返回空、写失败返回 false，不抛错。
// 这里保持一致；内部错误不进 API 面，避免调用方被迫写 err != nil 分支。
package files

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"howett.net/plist"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// 相对路径与参考样本一样以执行文件目录为基准，工程内资源不再误指向 HOME。
var rootMu sync.RWMutex
var rootDir = defaultRoot()

func defaultRoot() string {
	if executable, err := os.Executable(); err == nil {
		return filepath.Dir(executable)
	}
	return filepath.Dir(os.Args[0])
}

// SetRoot 覆盖相对路径基准（测试与桌面预览用）。
func SetRoot(dir string) {
	if dir != "" {
		rootMu.Lock()
		rootDir = dir
		rootMu.Unlock()
	}
}

// resolve 把相对路径拼到沙盒根上；已经是绝对路径的原样返回。
func resolve(p string) string {
	rootMu.RLock()
	root := rootDir
	rootMu.RUnlock()
	if p == "" {
		return root
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return p
	}
	return filepath.Join(root, p)
}

// Path 返回相对路径对应的绝对路径。
func Path(relativePath string) string { return resolve(relativePath) }

func IsFile(p string) bool {
	st, err := os.Stat(resolve(p))
	return err == nil && st.Mode().IsRegular()
}

func IsDir(p string) bool {
	st, err := os.Stat(resolve(p))
	return err == nil && st.IsDir()
}

// IsEmptyDir 在目录不存在时返回 false（不存在不等于空）。
func IsEmptyDir(p string) bool {
	f, err := os.Open(resolve(p))
	if err != nil {
		return false
	}
	defer f.Close()
	names, err := f.Readdirnames(1)
	return errors.Is(err, io.EOF) && len(names) == 0
}

func Exists(p string) bool {
	_, err := os.Stat(resolve(p))
	return err == nil
}

// Create 建一个空文件；父目录不存在时一并创建（AutoGo 的语义是"确保文件存在"）。
func Create(p string) bool {
	full := resolve(p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return false
	}
	f, err := os.OpenFile(full, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	return f.Close() == nil
}

// EnsureDir 建目录（含父级）；已存在也算成功。
func EnsureDir(p string) bool {
	return os.MkdirAll(resolve(p), 0o755) == nil
}

func Read(p string) string { return string(ReadBytes(p)) }

func ReadBytes(p string) []byte {
	b, err := os.ReadFile(resolve(p))
	if err != nil {
		return nil
	}
	return b
}

// Write 覆盖写入文本；父目录自动创建。
func Write(p, text string) {
	_ = os.MkdirAll(filepath.Dir(resolve(p)), 0o755)
	_ = os.WriteFile(resolve(p), []byte(text), 0o644)
}

func WriteBytes(p string, data []byte) {
	_ = os.MkdirAll(filepath.Dir(resolve(p)), 0o755)
	_ = os.WriteFile(resolve(p), data, 0o644)
}

func Append(p, text string) { AppendBytes(p, []byte(text)) }

func AppendBytes(p string, data []byte) {
	full := resolve(p)
	_ = os.MkdirAll(filepath.Dir(full), 0o755)
	f, err := os.OpenFile(full, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(data)
}

// Copy 复制文件；目标目录不存在时创建。目录复制不在这里做（AutoGo 也只复制文件）。
func Copy(fromPath, toPath string) bool {
	src := resolve(fromPath)
	dst := resolve(toPath)
	st, err := os.Stat(src)
	if err != nil || st.IsDir() {
		return false
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false
	}
	return os.WriteFile(dst, data, st.Mode()) == nil
}

// Move 移动/改名；跨分区时退化为「复制 + 删除」。
func Move(fromPath, toPath string) bool { return os.Rename(resolve(fromPath), resolve(toPath)) == nil }

// Rename 与参考样本一样将第二个参数作为目标路径，不再自动拼到旧文件同级目录。
func Rename(p, newName string) bool { return os.Rename(resolve(p), resolve(newName)) == nil }

func GetName(p string) string { return path.Base(strings.TrimSuffix(p, "/")) }

func GetNameWithoutExtension(p string) string {
	base := GetName(p)
	ext := path.Ext(base)
	return strings.TrimSuffix(base, ext)
}

func GetExtension(p string) string { return strings.TrimPrefix(path.Ext(p), ".") }

func GetMd5(p string) string {
	data := ReadBytes(p)
	if data == nil {
		return ""
	}
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

// Remove 删除文件或目录；不存在也算成功（幂等）。
func Remove(p string) bool {
	err := os.RemoveAll(resolve(p))
	return err == nil
}

// ListDir 返回目录下的名字（不含父路径），按名字排序，保证结果稳定。
func ListDir(p string) []string {
	entries, err := os.ReadDir(resolve(p))
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// ---- Plist ----
// 使用与样本专用 Plist 解析器同类的完整编解码器，支持 binary/XML、数组、字典、日期与 data。
func parsePlist(data []byte) map[string]interface{} {
	var out map[string]interface{}
	if _, err := plist.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}
func marshalPlist(data map[string]interface{}) []byte {
	body, err := plist.Marshal(data, plist.XMLFormat)
	if err != nil {
		return nil
	}
	return body
}
func ReadPlist(p string) map[string]interface{} {
	body := ReadBytes(p)
	if body == nil {
		return nil
	}
	return parsePlist(body)
}
func WritePlist(p string, data map[string]interface{}) bool {
	if data == nil {
		return false
	}
	body := marshalPlist(data)
	if body == nil {
		return false
	}
	target := resolve(p)
	if os.MkdirAll(filepath.Dir(target), 0755) != nil {
		return false
	}
	return os.WriteFile(target, body, 0644) == nil
}
func GetPlistValue(p, key string) interface{} { return ReadPlist(p)[key] }
func SetPlistValue(p, key string, value interface{}) bool {
	data := ReadPlist(p)
	if data == nil {
		if Exists(p) {
			return false
		}
		data = map[string]interface{}{}
	}
	data[key] = value
	return WritePlist(p, data)
}
