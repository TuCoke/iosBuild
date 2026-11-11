package storages

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"autogov1/v2/sdk/ios/files"
)

func newRoot(t *testing.T) {
	t.Helper()
	closeDatabase()
	files.SetRoot(t.TempDir())
	t.Cleanup(closeDatabase)
}

func TestPutGetContains(t *testing.T) {
	newRoot(t)
	Put("user", "name", "agov")
	if got := Get("user", "name"); got != "agov" {
		t.Fatalf("Get = %q", got)
	}
	if !Contains("user", "name") {
		t.Fatal("Contains 应为 true")
	}
	if Contains("user", "missing") {
		t.Fatal("Contains 不应为 true")
	}
	if got := Get("user", "missing"); got != "" {
		t.Fatalf("取不存在的键应返回空串，实际 %q", got)
	}
}

func TestOverwriteAndRemove(t *testing.T) {
	newRoot(t)
	Put("t", "k", "v1")
	Put("t", "k", "v2")
	if got := Get("t", "k"); got != "v2" {
		t.Fatalf("覆盖后 = %q", got)
	}
	Remove("t", "k")
	if Contains("t", "k") {
		t.Fatal("Remove 后不应存在")
	}
}

func TestGetAllAndClear(t *testing.T) {
	newRoot(t)
	Put("t", "a", "1")
	Put("t", "b", "2")
	all := GetAll("t")
	if len(all) != 2 || all["a"] != "1" || all["b"] != "2" {
		t.Fatalf("GetAll = %v", all)
	}
	Clear("t")
	if got := GetAll("t"); len(got) != 0 {
		t.Fatalf("Clear 后应空，实际 %v", got)
	}
}

// 表之间互不影响：写 user 不该污染 config。
func TestTablesIsolated(t *testing.T) {
	newRoot(t)
	Put("user", "k", "u")
	Put("config", "k", "c")
	if Get("user", "k") != "u" || Get("config", "k") != "c" {
		t.Fatal("两张表串了")
	}
}

// 持久化：落盘后再 load 应能读回来（模拟进程重启）。
func TestPersistsAcrossReload(t *testing.T) {
	newRoot(t)
	Put("t", "k", "v")
	data := load("t")
	if data["k"] != "v" {
		t.Fatalf("落盘后 load = %v", data)
	}
}

// 第二个数据库连接能读到提交结果，且文件头确为 SQLite，不能以 JSON 冒充落库。
func TestSQLiteFileAndDefaultTable(t *testing.T) {
	newRoot(t)
	Put("", "k", "原始值")
	raw, err := os.ReadFile(files.Path("storages.db"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), "SQLite format 3\x00") {
		t.Fatal("不是 SQLite 数据库")
	}
	external, err := sql.Open(sqliteDriver, files.Path("storages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	var value string
	if err = external.QueryRow("SELECT value FROM storages WHERE key = ?", "k").Scan(&value); err != nil || value != "原始值" {
		t.Fatalf("提交结果对其他连接不可见: %q %v", value, err)
	}
	Clear("")
	var count int
	if err = external.QueryRow("SELECT COUNT(*) FROM storages").Scan(&count); err != nil || count != 0 {
		t.Fatalf("Clear 删除了表或没有清空: %d %v", count, err)
	}
}
func TestDatabaseReopensAndQuotesTable(t *testing.T) {
	newRoot(t)
	name := `odd"; DROP TABLE storages;--`
	Put(name, "k", "v")
	closeDatabase()
	if Get(name, "k") != "v" {
		t.Fatal("关闭重连丢失数据或表名未正确引用")
	}
}
