package script

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

// preprocessEmbed 处理 string、[]byte 和 embed.FS，保留其余源码语义。
func preprocessEmbed(filename string, source []byte, files map[string][]byte) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, source, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	embedAlias := "embed"
	var embedImport *ast.ImportSpec
	for _, imp := range file.Imports {
		if imp.Path.Value == `"embed"` {
			embedImport = imp
			if imp.Name != nil {
				embedAlias = imp.Name.Name
			}
		}
	}
	changed := false
	needsFS := false
	for _, decl := range file.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.VAR {
			continue
		}
		for _, spec := range group.Specs {
			v := spec.(*ast.ValueSpec)
			comments := v.Doc
			if comments == nil {
				comments = group.Doc
			}
			if comments == nil {
				continue
			}
			var patterns []string
			for _, comment := range comments.List {
				if !strings.HasPrefix(comment.Text, "//go:embed ") {
					continue
				}
				p, e := embedPatterns(strings.TrimPrefix(comment.Text, "//go:embed "))
				if e != nil {
					return nil, e
				}
				patterns = append(patterns, p...)
				comment.Text = "// 资源已由解释器工程加载器展开。"
			}
			if len(patterns) == 0 {
				continue
			}
			if embedImport == nil || len(v.Names) != 1 || len(v.Values) != 0 {
				return nil, fmt.Errorf("%s: go:embed 需要 embed 导入和单个无初值变量", filename)
			}
			selected := map[string]string{}
			for _, pattern := range patterns {
				all := strings.HasPrefix(pattern, "all:")
				pattern = strings.TrimPrefix(pattern, "all:")
				if !fs.ValidPath(pattern) || strings.Contains(pattern, "\\") {
					return nil, fmt.Errorf("embed 路径无效: %s", pattern)
				}
				matches := 0
				for name, data := range files {
					relative := strings.TrimPrefix(name, path.Dir(filename)+"/")
					if path.Dir(filename) == "." {
						relative = name
					}
					if relative == name && path.Dir(filename) != "." {
						continue
					}
					if !all && hiddenPath(relative) {
						continue
					}
					hit := false
					for candidate := relative; candidate != "."; candidate = path.Dir(candidate) {
						match, e := path.Match(pattern, candidate)
						if e != nil {
							return nil, e
						}
						if match {
							hit = true
							break
						}
					}
					if hit {
						selected[relative] = string(data)
						matches++
					}
				}
				if matches == 0 {
					return nil, fmt.Errorf("%s: embed 未匹配文件: %s", filename, pattern)
				}
			}
			names := make([]string, 0, len(selected))
			for n := range selected {
				names = append(names, n)
			}
			sort.Strings(names)
			var expression string
			switch typ := v.Type.(type) {
			case *ast.Ident:
				if typ.Name != "string" || len(names) != 1 {
					return nil, fmt.Errorf("embed string 需要单个文件")
				}
				expression = strconv.Quote(selected[names[0]])
			case *ast.ArrayType:
				if typ.Len != nil {
					return nil, fmt.Errorf("embed 数组类型不支持")
				}
				id, ok := typ.Elt.(*ast.Ident)
				if !ok || id.Name != "byte" || len(names) != 1 {
					return nil, fmt.Errorf("embed []byte 需要单个文件")
				}
				expression = "[]byte(" + strconv.Quote(selected[names[0]]) + ")"
			case *ast.SelectorExpr:
				base, ok := typ.X.(*ast.Ident)
				if !ok || base.Name != embedAlias || typ.Sel.Name != "FS" {
					return nil, fmt.Errorf("embed 类型不支持")
				}
				needsFS = true
				var b strings.Builder
				b.WriteString(embedAlias + ".New(map[string]string{")
				for _, n := range names {
					b.WriteString(strconv.Quote(n) + ":" + strconv.Quote(selected[n]) + ",")
				}
				b.WriteString("})")
				expression = b.String()
			default:
				return nil, fmt.Errorf("embed 变量类型不支持")
			}
			expr, e := parser.ParseExpr(expression)
			if e != nil {
				return nil, e
			}
			v.Values = []ast.Expr{expr}
			changed = true
		}
	}
	if !changed {
		return source, nil
	}
	if needsFS {
		embedImport.Path.Value = strconv.Quote("autogov1/v2/sdk/ios/script/embedfs")
		if embedImport.Name == nil {
			embedImport.Name = ast.NewIdent("embed")
		}
	}
	var out bytes.Buffer
	if err = format.Node(&out, fset, file); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func hiddenPath(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || strings.HasPrefix(part, "_") {
			return true
		}
	}
	return false
}
func embedPatterns(text string) ([]string, error) {
	var out []string
	for text = strings.TrimSpace(text); text != ""; text = strings.TrimSpace(text) {
		if text[0] == '"' || text[0] == '`' {
			quote := text[0]
			end := 1
			for end < len(text) {
				if text[end] == quote {
					break
				}
				if text[end] == '\\' && quote == '"' {
					end++
				}
				end++
			}
			if end >= len(text) {
				return nil, fmt.Errorf("embed 路径引号未闭合")
			}
			value, err := strconv.Unquote(text[:end+1])
			if err != nil {
				return nil, err
			}
			out = append(out, value)
			text = text[end+1:]
		} else {
			end := strings.IndexAny(text, " \t\r\n")
			if end < 0 {
				out = append(out, text)
				break
			}
			out = append(out, text[:end])
			text = text[end:]
		}
	}
	return out, nil
}
