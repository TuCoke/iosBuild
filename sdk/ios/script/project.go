package script

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"autogov1/v2/sdk/ios/script/embedfs"
)

const maxProjectBytes = 32 << 20
const maxProjectFiles = 1024

// sourceFS 仅用于解释器源码加载。绝对源码路径在此映射到虚拟根，不改变进程工作目录。
type sourceFS struct{ embedfs.FS }

func (f sourceFS) Open(name string) (fs.File, error) {
	name = strings.TrimPrefix(filepath.ToSlash(name), "/")
	name = path.Clean(name)
	if name == ".." || strings.HasPrefix(name, "../") {
		return nil, fs.ErrPermission
	}
	return f.FS.Open(name)
}

func (r *Runner) RunFile(ctx context.Context, filename string) (Result, error) {
	if strings.EqualFold(filepath.Ext(filename), ".zip") {
		b, err := readLimitedFile(filename, maxProjectBytes)
		if err != nil {
			return Result{}, err
		}
		return r.RunZip(ctx, b, "")
	}
	return r.RunProject(ctx, os.DirFS(filepath.Dir(filename)), filepath.Base(filename))
}
func readLimitedFile(filename string, limit int64) ([]byte, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = fmt.Errorf("文件超过限制: %s", filename)
	}
	return b, err
}

func (r *Runner) RunProject(ctx context.Context, filesystem fs.FS, entry string) (Result, error) {
	if ctx == nil || filesystem == nil {
		return Result{}, fmt.Errorf("工程 context 和文件系统不能为空")
	}
	files := map[string][]byte{}
	total := 0
	err := fs.WalkDir(filesystem, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("工程不接受符号链接: %s", name)
		}
		if len(files) >= maxProjectFiles {
			return fmt.Errorf("工程文件超过 %d 个", maxProjectFiles)
		}
		f, e := filesystem.Open(name)
		if e != nil {
			return e
		}
		b, e := io.ReadAll(io.LimitReader(f, int64(maxProjectBytes-total)+1))
		f.Close()
		if e != nil {
			return e
		}
		total += len(b)
		if total > maxProjectBytes {
			return fmt.Errorf("工程超过 32 MiB")
		}
		files[name] = b
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return r.runProject(ctx, files, entry)
}

func (r *Runner) RunZip(ctx context.Context, data []byte, entry string) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("工程 context 不能为空")
	}
	if len(data) > maxProjectBytes {
		return Result{}, fmt.Errorf("ZIP 超过 32 MiB")
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Result{}, err
	}
	if len(z.File) > maxProjectFiles {
		return Result{}, fmt.Errorf("ZIP 文件过多")
	}
	files := map[string][]byte{}
	total := 0
	for _, f := range z.File {
		if err = ctx.Err(); err != nil {
			return Result{}, err
		}
		name := f.Name
		if f.FileInfo().IsDir() {
			continue
		}
		if !fs.ValidPath(name) || strings.Contains(name, "\\") || f.Mode()&fs.ModeSymlink != 0 {
			return Result{}, fmt.Errorf("ZIP 路径无效: %s", name)
		}
		if _, ok := files[name]; ok {
			return Result{}, fmt.Errorf("ZIP 路径重复: %s", name)
		}
		if f.UncompressedSize64 > uint64(maxProjectBytes-total) {
			return Result{}, fmt.Errorf("ZIP 解压大小超过限制")
		}
		reader, e := f.Open()
		if e != nil {
			return Result{}, e
		}
		b, e := io.ReadAll(io.LimitReader(reader, int64(maxProjectBytes-total)+1))
		reader.Close()
		if e != nil {
			return Result{}, e
		}
		total += len(b)
		if total > maxProjectBytes {
			return Result{}, fmt.Errorf("ZIP 解压大小超过限制")
		}
		files[name] = b
	}
	// 常见导出包外面有一层工程目录；仅在所有文件共享此层时去掉，不能猜测多个入口。
	if _, ok := files["main.go"]; !ok {
		prefix := ""
		same := true
		for n := range files {
			first, _, found := strings.Cut(n, "/")
			if !found {
				same = false
				break
			}
			if prefix == "" {
				prefix = first
			} else if prefix != first {
				same = false
				break
			}
		}
		if same && prefix != "" {
			trimmed := map[string][]byte{}
			for n, b := range files {
				trimmed[strings.TrimPrefix(n, prefix+"/")] = b
			}
			files = trimmed
		}
	}
	return r.runProject(ctx, files, entry)
}

var modulePattern = regexp.MustCompile(`(?m)^\s*module\s+(\S+)`)

func (r *Runner) runProject(ctx context.Context, files map[string][]byte, entry string) (Result, error) {
	if entry == "" {
		entry = "main.go"
	}
	entry = path.Clean(entry)
	if !fs.ValidPath(entry) || !strings.HasSuffix(entry, ".go") {
		return Result{}, fmt.Errorf("入口路径无效: %s", entry)
	}
	if _, ok := files[entry]; !ok {
		return Result{}, fmt.Errorf("找不到入口文件: %s", entry)
	}
	module := "project"
	if match := modulePattern.FindSubmatch(files["go.mod"]); len(match) > 1 {
		module = strings.Trim(string(match[1]), "\"")
	}
	if !fs.ValidPath(module) {
		return Result{}, fmt.Errorf("工程模块路径无效")
	}
	prepared := map[string]string{}
	mainEntry := ""
	for name, b := range files {
		if strings.HasSuffix(name, ".go") {
			if len(b) > 4<<20 {
				return Result{}, fmt.Errorf("源码过大: %s", name)
			}
			// 包执行不能让后读入的 main 静默覆盖前一个入口；在任何 init/设备动作前拒绝。
			if path.Dir(name) == path.Dir(entry) && !strings.HasSuffix(name, "_test.go") {
				tree, parseErr := parser.ParseFile(token.NewFileSet(), name, b, 0)
				if parseErr != nil {
					return Result{}, parseErr
				}
				if tree.Name.Name == "main" {
					for _, declaration := range tree.Decls {
						if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "main" {
							if mainEntry != "" {
								return Result{}, fmt.Errorf("脚本包含重复 main 入口: %s 与 %s；不同任务请使用独立工程目录", mainEntry, name)
							}
							mainEntry = name
						}
					}
				}
			}
			var err error
			b, err = preprocessEmbed(name, b, files)
			if err != nil {
				return Result{}, err
			}
		}
		prepared[path.Join("src", module, name)] = string(b)
	}
	// 按包执行，支持 main.go 引用同目录其他 Go 文件和工程内部包。
	packagePath := module
	if dir := path.Dir(entry); dir != "." {
		packagePath = path.Join(module, dir)
	}
	return r.execute(ctx, sourceFS{embedfs.New(prepared)}, packagePath, "")
}
