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
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// rootDir 是相对路径的基准（应用沙盒根）。宿主可用 SetRoot 覆盖。
var rootDir = defaultRoot()

// defaultRoot 取 App 沙盒的 Documents 上级（即容器根）。
// iOS 上 HOME 就是容器根，Go 的 os.UserHomeDir 会拿到它；
// 桌面平台跑单测时退到当前工作目录，保证同一套逻辑可测。
func defaultRoot() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// SetRoot 覆盖相对路径基准（测试与桌面预览用）。
func SetRoot(dir string) {
	if dir != "" {
		rootDir = dir
	}
}

// resolve 把相对路径拼到沙盒根上；已经是绝对路径的原样返回。
func resolve(p string) string {
	if p == "" {
		return rootDir
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return p
	}
	return filepath.Join(rootDir, p)
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
	return err == nil && len(names) == 0
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
func Move(fromPath, toPath string) bool {
	src, dst := resolve(fromPath), resolve(toPath)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false
	}
	if err := os.Rename(src, dst); err == nil {
		return true
	}
	if !Copy(src, dst) {
		return false
	}
	return os.RemoveAll(src) == nil
}

// Rename 在同一目录内改名。
func Rename(p, newName string) bool {
	full := resolve(p)
	return Move(full, filepath.Join(filepath.Dir(full), newName))
}

func GetName(p string) string { return path.Base(strings.TrimSuffix(p, "/")) }

func GetNameWithoutExtension(p string) string {
	base := GetName(p)
	ext := path.Ext(base)
	return strings.TrimSuffix(base, ext)
}

func GetExtension(p string) string { return path.Ext(p) }

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

// ---- plist ----
//
// 【只支持 XML plist】iOS 的二进制 plist（bplist00）结构复杂且没有标准库支持，
// 而我们要读写的都是自己生成的配置；遇到二进制 plist 返回 nil/ false，
// 让调用方知道"这个文件我读不了"，而不是给一份看起来正常的空配置。

type plistDict struct {
	XMLName xml.Name    `xml:"dict"`
	Keys    []string    `xml:"key"`
	Values  []plistNode `xml:",any"`
}

type plistNode struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

func parsePlist(data []byte) map[string]interface{} {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "bplist") {
		return nil
	}
	var root struct {
		XMLName xml.Name    `xml:"plist"`
		Dict    plistDict   `xml:"dict"`
	}
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil
	}
	dict := root.Dict
	if len(dict.Keys) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(dict.Keys))
	for i, key := range dict.Keys {
		if i >= len(dict.Values) {
			break
		}
		v := dict.Values[i]
		text := strings.TrimSpace(v.Value)
		switch v.XMLName.Local {
		case "string":
			out[key] = text
		case "integer":
			n, _ := strconv.ParseInt(text, 10, 64)
			out[key] = int(n)
		case "real":
			f, _ := strconv.ParseFloat(text, 64)
			out[key] = f
		case "true":
			out[key] = true
		case "false":
			out[key] = false
		}
	}
	return out
}

func marshalPlist(data map[string]interface{}) []byte {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	for _, k := range keys {
		b.WriteString("\t<key>" + xmlEscape(k) + "</key>\n")
		switch v := data[k].(type) {
		case bool:
			if v {
				b.WriteString("\t<true/>\n")
			} else {
				b.WriteString("\t<false/>\n")
			}
		case int:
			b.WriteString("\t<integer>" + strconv.Itoa(v) + "</integer>\n")
		case int64:
			b.WriteString("\t<integer>" + strconv.FormatInt(v, 10) + "</integer>\n")
		case float64:
			b.WriteString("\t<real>" + strconv.FormatFloat(v, 'f', -1, 64) + "</real>\n")
		default:
			b.WriteString("\t<string>" + xmlEscape(fmt.Sprint(v)) + "</string>\n")
		}
	}
	b.WriteString("</dict>\n</plist>\n")
	return []byte(b.String())
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func ReadPlist(p string) map[string]interface{} {
	data := ReadBytes(p)
	if data == nil {
		return nil
	}
	return parsePlist(data)
}

func WritePlist(p string, data map[string]interface{}) bool {
	if data == nil {
		return false
	}
	WriteBytes(p, marshalPlist(data))
	return Exists(p)
}

func GetPlistValue(p, key string) interface{} {
	dict := ReadPlist(p)
	if dict == nil {
		return nil
	}
	return dict[key]
}

// SetPlistValue 读-改-写；文件不存在则新建。读到了但不是合法 plist 时返回 false，
// 避免把用户原有配置覆盖成只有一条键的空壳。
func SetPlistValue(p, key string, value interface{}) bool {
	dict := ReadPlist(p)
	if dict == nil {
		if Exists(p) {
			return false
		}
		dict = map[string]interface{}{}
	}
	dict[key] = value
	return WritePlist(p, dict)
}

// errNotFound 供内部断言使用（对外 API 不暴露 error）。
var errNotFound = errors.New("not found")
