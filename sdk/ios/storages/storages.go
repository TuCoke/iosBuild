// Package storages 按参考样本的 database/sql + SQLite 路径保存分表字符串键值。
package storages

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"autogov1/v2/sdk/ios/files"
)

var databaseState struct {
	sync.Mutex
	path   string
	handle *sql.DB
}

// database 复用当前工作根对应的连接。根变化时关闭旧连接，不跨工程复用数据库。
// 参考样本使用执行目录下的 storages.db、默认 storages 表及 key/value 两列。
func database() (*sql.DB, error) {
	databaseState.Lock()
	defer databaseState.Unlock()
	filename := files.Path("storages.db")
	if databaseState.handle != nil && databaseState.path == filename {
		return databaseState.handle, nil
	}
	if databaseState.handle != nil {
		_ = databaseState.handle.Close()
		databaseState.handle = nil
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return nil, err
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(filename)}
	if len(uri.Path) > 1 && uri.Path[1] == ':' {
		uri.Path = "/" + uri.Path
	}
	query := uri.Query()
	configureSQLite(query)
	uri.RawQuery = query.Encode()
	handle, err := sql.Open(sqliteDriver, uri.String())
	if err != nil {
		return nil, err
	}
	handle.SetMaxOpenConns(1)
	if err = handle.Ping(); err != nil {
		handle.Close()
		return nil, err
	}
	databaseState.path = filename
	databaseState.handle = handle
	return handle, nil
}
func closeDatabase() {
	databaseState.Lock()
	defer databaseState.Unlock()
	if databaseState.handle != nil {
		_ = databaseState.handle.Close()
	}
	databaseState.handle = nil
	databaseState.path = ""
}
func tableName(table string) string {
	if table == "" {
		table = "storages"
	}
	return `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
}
func Get(table, key string) string {
	db, err := database()
	if err != nil {
		return ""
	}
	var value string
	_ = db.QueryRow("SELECT value FROM "+tableName(table)+" WHERE key = ?", key).Scan(&value)
	return value
}
func Put(table, key, value string) {
	db, err := database()
	if err != nil {
		return
	}
	name := tableName(table)
	tx, err := db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec("CREATE TABLE IF NOT EXISTS " + name + " (key TEXT NOT NULL PRIMARY KEY, value TEXT)"); err != nil {
		return
	}
	if _, err = tx.Exec("INSERT OR REPLACE INTO "+name+" (key, value) VALUES (?, ?)", key, value); err != nil {
		return
	}
	_ = tx.Commit()
}
func Remove(table, key string) {
	db, err := database()
	if err != nil {
		return
	}
	_, _ = db.Exec("DELETE FROM "+tableName(table)+" WHERE key = ?", key)
}
func Contains(table, key string) bool {
	db, err := database()
	if err != nil {
		return false
	}
	var count int
	return db.QueryRow("SELECT COUNT(*) FROM "+tableName(table)+" WHERE key = ?", key).Scan(&count) == nil && count > 0
}
func GetAll(table string) map[string]string { return load(table) }
func load(table string) map[string]string {
	out := map[string]string{}
	db, err := database()
	if err != nil {
		return out
	}
	rows, err := db.Query("SELECT key, value FROM " + tableName(table))
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if rows.Scan(&key, &value) == nil {
			out[key] = value
		}
	}
	return out
}
func Clear(table string) {
	db, err := database()
	if err != nil {
		return
	}
	_, _ = db.Exec("DELETE FROM " + tableName(table))
}
