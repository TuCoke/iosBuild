// Package storages 对齐 AutoGo iOS 的本地存储 API（6 个函数）。
//
// 语义：按「表」分组存键值，值一律是字符串。表落成沙盒里的一个 JSON 文件
// （<root>/.storages/<表名>.json），进程重启后仍在。
//
// 【为什么不用 NSUserDefaults】那是原生桥的另一条路径，每次 Put 都要过一次 JSON 往返；
// 而这张表的读写都在 App 自己的沙盒里，直接用文件最省，也方便备份/导出。
package storages

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"autogov1/v2/sdk/ios/files"
)

var mu sync.Mutex

func tableFile(table string) string {
	if table == "" {
		table = "default"
	}
	// 表名要做最小净化：不允许路径分隔符，避免 call 方把整个沙盒写穿。
	safe := make([]rune, 0, len(table))
	for _, r := range table {
		if r == '/' || r == '\\' || r == 0 || r == '.' && len(safe) == 0 {
			safe = append(safe, '_')
			continue
		}
		safe = append(safe, r)
	}
	return files.Path(filepath.Join(".storages", string(safe)+".json"))
}

func load(table string) map[string]string {
	data, err := os.ReadFile(tableFile(table))
	if err != nil || len(data) == 0 {
		return map[string]string{}
	}
	out := map[string]string{}
	if json.Unmarshal(data, &out) != nil {
		return map[string]string{}
	}
	return out
}

func save(table string, data map[string]string) {
	p := tableFile(table)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	_ = os.WriteFile(p, b, 0o644)
}

// Get 取不到返回空串（与 AutoGo 一致，不区分"没有"和"空值"）。
func Get(table, key string) string {
	mu.Lock()
	defer mu.Unlock()
	return load(table)[key]
}

func Put(table, key, value string) {
	mu.Lock()
	defer mu.Unlock()
	data := load(table)
	data[key] = value
	save(table, data)
}

func Remove(table, key string) {
	mu.Lock()
	defer mu.Unlock()
	data := load(table)
	if _, ok := data[key]; !ok {
		return
	}
	delete(data, key)
	save(table, data)
}

func Contains(table, key string) bool {
	mu.Lock()
	defer mu.Unlock()
	_, ok := load(table)[key]
	return ok
}

// GetAll 返回整张表的副本，调用方改它不会影响存储。
func GetAll(table string) map[string]string {
	mu.Lock()
	defer mu.Unlock()
	data := load(table)
	out := make(map[string]string, len(data))
	for k, v := range data {
		out[k] = v
	}
	return out
}

func Clear(table string) {
	mu.Lock()
	defer mu.Unlock()
	_ = os.Remove(tableFile(table))
}
