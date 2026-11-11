//go:build ios && cgo

package storages

import (
	_ "github.com/mattn/go-sqlite3"
	"net/url"
)

// 真机采用与样本相同的 SQLite3 cgo 驱动技术栈。
const sqliteDriver = "sqlite3"

func configureSQLite(query url.Values) { query.Set("_busy_timeout", "5000") }
