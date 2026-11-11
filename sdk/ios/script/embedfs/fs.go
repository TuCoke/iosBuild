// Package embedfs 为解释执行的工程提供只读资源文件系统，不依赖编译器处理 go:embed。
package embedfs

import (
	"bytes"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
)

type FS struct{ files map[string]string }

func New(files map[string]string) FS {
	copy := make(map[string]string, len(files))
	for k, v := range files {
		copy[k] = v
	}
	return FS{files: copy}
}
func (f FS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if data, ok := f.files[name]; ok {
		return &file{Reader: bytes.NewReader([]byte(data)), info: info{name: path.Base(name), size: int64(len(data))}}, nil
	}
	entries, err := f.ReadDir(name)
	if err != nil {
		return nil, err
	}
	return &directory{info: info{name: path.Base(name), dir: true}, entries: entries}, nil
}
func (f FS) ReadFile(name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrInvalid
	}
	v, ok := f.files[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(v), nil
}
func (f FS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrInvalid
	}
	if _, ok := f.files[name]; ok {
		return nil, fs.ErrInvalid
	}
	prefix := ""
	if name != "." {
		prefix = name + "/"
	}
	children := map[string]info{}
	for key, value := range f.files {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := strings.TrimPrefix(key, prefix)
		first, _, dir := strings.Cut(rest, "/")
		if first == "" {
			continue
		}
		children[first] = info{name: first, dir: dir, size: int64(len(value))}
	}
	if len(children) == 0 && name != "." {
		return nil, fs.ErrNotExist
	}
	names := make([]string, 0, len(children))
	for n := range children {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]fs.DirEntry, 0, len(names))
	for _, n := range names {
		out = append(out, children[n])
	}
	return out, nil
}

type info struct {
	name string
	size int64
	dir  bool
}

func (i info) Name() string { return i.name }
func (i info) Size() int64 {
	if i.dir {
		return 0
	}
	return i.size
}
func (i info) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0555
	}
	return 0444
}
func (i info) ModTime() time.Time         { return time.Time{} }
func (i info) IsDir() bool                { return i.dir }
func (i info) Sys() any                   { return nil }
func (i info) Type() fs.FileMode          { return i.Mode().Type() }
func (i info) Info() (fs.FileInfo, error) { return i, nil }

type file struct {
	*bytes.Reader
	info info
}

func (f *file) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *file) Close() error               { return nil }

type directory struct {
	info     info
	entries  []fs.DirEntry
	position int
}

func (d *directory) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *directory) Close() error               { return nil }
func (d *directory) Read([]byte) (int, error)   { return 0, fs.ErrInvalid }
func (d *directory) ReadDir(n int) ([]fs.DirEntry, error) {
	if d.position >= len(d.entries) && n > 0 {
		return nil, io.EOF
	}
	end := len(d.entries)
	if n > 0 && d.position+n < end {
		end = d.position + n
	}
	v := d.entries[d.position:end]
	d.position = end
	return v, nil
}
