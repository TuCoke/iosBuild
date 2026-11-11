//go:build !ios || !cgo

package storages

import (
	_ "modernc.org/sqlite"
	"net/url"
)

// 非 iOS 检查仍使用真实 SQLite 文件；不依赖 Windows 本机 C 编译器，也不退回 JSON。
const sqliteDriver = "sqlite"

func configureSQLite(query url.Values) { query.Set("_pragma", "busy_timeout(5000)") }
