package main

import (
	"os"
	"path/filepath"
	"testing"
)

// 构造器返回指针、结构体和导出变量不能被生成器静默忽略。
func TestScanPublicSymbols(t *testing.T) {
	dir := t.TempDir()
	source := `package example
 type Object struct{}
 func New()*Object{return nil}
 func (o *Object) Method(){}
 var Current *Object
 const Version="x"
 func private(){}
 func Generic[T any](v T)T{return v}
 `
	if err := os.WriteFile(filepath.Join(dir, "api.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	symbols, err := scanPackage(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"Object": "type", "New": "func", "Current": "var", "Version": "const"}
	if len(symbols) != len(want) {
		t.Fatal(symbols)
	}
	for _, s := range symbols {
		if want[s.Name] != s.Kind {
			t.Fatal(s)
		}
	}
}
