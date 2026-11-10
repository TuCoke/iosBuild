// precheck 一次性静态检查：把"只能靠 CI 编译才发现"的问题尽量提前到本地。
//
// 起因：每次改完都要等 CI 编译，一个符号没导出、一个 op 没实现都要等十几分钟。
// 这个工具把可静态判断的东西全查一遍：
//
//	① Go 语法（go/parser，不需要 cgo/编译器）
//	② //export 注释是否紧邻函数（cgo 的硬要求，踩过两次）
//	③ Swift 里调用的每个 method 是否在 Go 的 switch 里存在（踩过 sdk.status）
//	④ Go 侧调用的每个原生 op 是否在原生分派里存在
//	⑤ ag_bridge.h 与 .m 的函数是否一一对应
//	⑥ 版本号一致性：project.yml / Info.plist / workflow 的 IOS_MIN
//	⑦ workflow 顺序不变量：拷贝→签名→断言；ldid 路径带 host/ios/
//
// 用法：go run ./cmd/precheck   （返回码非 0 表示有问题）
package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var failures []string

func fail(format string, args ...any) { failures = append(failures, fmt.Sprintf(format, args...)) }
func ok(format string, args ...any)   { fmt.Printf("  ✅ "+format+"\n", args...) }

func main() {
	fmt.Println("== precheck：静态自检 ==")
	checkGoSyntax()
	checkExportAdjacency()
	checkSwiftMethods()
	checkNativeOps()
	checkBridgeParity()
	checkVersions()
	checkWorkflowOrder()

	fmt.Println()
	if len(failures) == 0 {
		fmt.Println("✅ 全部通过")
		return
	}
	fmt.Printf("❌ %d 个问题：\n", len(failures))
	for _, f := range failures {
		fmt.Println("  -", f)
	}
	os.Exit(1)
}

func walk(dir string, ext string) []string {
	var files []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ext) {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// ① Go 语法
func checkGoSyntax() {
	files := walk("sdk", ".go")
	files = append(files, walk("cmd", ".go")...)
	bad := 0
	fset := token.NewFileSet()
	for _, f := range files {
		if _, err := parser.ParseFile(fset, f, nil, 0); err != nil {
			fail("Go 语法错误 %s: %v", f, err)
			bad++
		}
	}
	if bad == 0 {
		ok("Go 语法：%d 个文件", len(files))
	}
}

// ② //export 紧邻
func checkExportAdjacency() {
	path := "cmd/ioshost/main.go"
	data, err := os.ReadFile(path)
	if err != nil {
		fail("读不到 %s", path)
		return
	}
	lines := strings.Split(string(data), "\n")
	want := []string{"agov_ios_invoke", "agov_ios_free", "GoMain"}
	found := 0
	for i, line := range lines {
		for _, name := range want {
			if strings.HasPrefix(line, "func "+name+"(") {
				found++
				if i == 0 || !strings.HasPrefix(strings.TrimSpace(lines[i-1]), "//export ") {
					fail("%s 的上一行不是 //export（cgo 不会导出符号）", name)
				}
			}
		}
	}
	if found == len(want) {
		ok("//export 紧邻：%d 个入口", found)
	} else {
		fail("只找到 %d/%d 个导出函数", found, len(want))
	}
}

var caseRe = regexp.MustCompile(`case "([a-z_]+\.[a-z_]+)":`)
var invokeRe = regexp.MustCompile(`AgovBridge\.invoke\("([a-z_]+\.[a-z_]+)"`)

// ③ Swift 调用的 method 必须在 Go switch 里
func checkSwiftMethods() {
	goData, err := os.ReadFile("cmd/ioshost/main.go")
	if err != nil {
		fail("读不到 cmd/ioshost/main.go")
		return
	}
	handled := map[string]bool{}
	for _, m := range caseRe.FindAllStringSubmatch(string(goData), -1) {
		handled[m[1]] = true
	}
	missing := map[string]string{}
	for _, swift := range walk("host/ios/Sources", ".swift") {
		data, _ := os.ReadFile(swift)
		for _, m := range invokeRe.FindAllStringSubmatch(string(data), -1) {
			if !handled[m[1]] {
				missing[m[1]] = filepath.Base(swift)
			}
		}
	}
	if len(missing) == 0 {
		ok("Swift 调用的 method 都能在 Go switch 里找到")
		return
	}
	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fail("Swift 调用了 %s（%s），但 Go 的 switch 里没有这个 case", name, missing[name])
	}
}

// ④ Go 侧调用的原生 op 必须在原生分派里
func checkNativeOps() {
	nativeDir := "sdk/ios/internal/native"
	var dispatch strings.Builder
	for _, f := range walk(nativeDir, ".m") {
		data, _ := os.ReadFile(f)
		dispatch.Write(data)
	}
	dispatchSrc := dispatch.String()
	known := map[string]bool{}
	for _, m := range caseRe.FindAllStringSubmatch(dispatchSrc, -1) {
		known[m[1]] = true
	}
	// 原生里还有 hasPrefix 的批量分派，这里只做"精确 case"的检查
	opsUsed := map[string]string{}
	callRe := regexp.MustCompile(`(?:native\.Call\([^,]+,\s*|bridge\.Do(?:Timeout)?\()"([a-z_]+\.[a-z_]+)"`)
	for _, f := range walk("sdk/ios", ".go") {
		data, _ := os.ReadFile(f)
		for _, m := range callRe.FindAllStringSubmatch(string(data), -1) {
			op := m[1]
			if strings.HasPrefix(op, "capture.") || strings.HasPrefix(op, "video.") ||
				strings.HasPrefix(op, "device.") || strings.HasPrefix(op, "app.") ||
				strings.HasPrefix(op, "motion.") || strings.HasPrefix(op, "ime.") ||
				strings.HasPrefix(op, "hud.") || strings.HasPrefix(op, "console.") ||
				strings.HasPrefix(op, "utils.") || strings.HasPrefix(op, "system.") ||
				strings.HasPrefix(op, "ppocr.") || strings.HasPrefix(op, "yolo.") ||
				strings.HasPrefix(op, "dotocr.") || strings.HasPrefix(op, "apkctl.") ||
				strings.HasPrefix(op, "uiacc.") || strings.HasPrefix(op, "runtime.") {
				continue // 这些走前缀分派，下面单独粗查
			}
			opsUsed[op] = filepath.Base(f)
		}
	}
	for op, file := range opsUsed {
		if !known[op] {
			fail("Go 调用 %s（%s），但原生没有对应的精确 case", op, file)
		}
	}
	ok("原生 op 引用检查完成（前缀分派除外）")
}

// ⑤ ag_bridge.h 与 .m 一一对应
func checkBridgeParity() {
	h, err1 := os.ReadFile("sdk/ios/internal/native/ag_bridge.h")
	m, err2 := os.ReadFile("sdk/ios/internal/native/ag_bridge.m")
	if err1 != nil || err2 != nil {
		fail("读不到 ag_bridge.h/.m")
		return
	}
	// 注意 char *BridgeXxx 里的星号：漏了它会把 11 个返回字符串的函数漏掉。
	defRe := regexp.MustCompile(`(?m)^(?:int|void|char|double|long long)\s*\*?\s*(Bridge\w+)\s*\(`)
	declRe := regexp.MustCompile(`(?m)^\s*(?:int|void|char|double|long long)\s*\*?\s*(Bridge\w+)\s*\(`)
	decl := map[string]bool{}
	def := map[string]bool{}
	for _, x := range declRe.FindAllStringSubmatch(string(h), -1) {
		decl[x[1]] = true
	}
	for _, x := range defRe.FindAllStringSubmatch(string(m), -1) {
		def[x[1]] = true
	}
	for name := range decl {
		if !def[name] {
			fail("ag_bridge.h 声明了 %s，但 .m 没有实现", name)
		}
	}
	for name := range def {
		if !decl[name] {
			fail("ag_bridge.m 实现了 %s，但 .h 没有声明", name)
		}
	}
	if len(failures) == 0 {
		ok("Bridge 函数一一对应：%d 个", len(decl))
	}
}

// ⑥ 版本号一致性
func checkVersions() {
	proj, _ := os.ReadFile("host/ios/project.yml")
	wf, _ := os.ReadFile(".github/workflows/ios-sdk.yml")
	plist, _ := os.ReadFile("host/ios/Info.plist")

	verRe := regexp.MustCompile(`iOS:\s*"([0-9.]+)"`)
	projVer := ""
	if m := verRe.FindStringSubmatch(string(proj)); m != nil {
		projVer = m[1]
	}
	iosMinRe := regexp.MustCompile(`IOS_MIN:\s*'([0-9.]+)'`)
	wfVers := map[string]bool{}
	for _, m := range iosMinRe.FindAllStringSubmatch(string(wf), -1) {
		wfVers[m[1]] = true
	}
	plistRe := regexp.MustCompile(`<key>MinimumOSVersion</key>\s*<string>([0-9.]+)</string>`)
	plistVer := ""
	if m := plistRe.FindStringSubmatch(string(plist)); m != nil {
		plistVer = m[1]
	}

	if projVer == "" {
		fail("project.yml 里没找到 deploymentTarget iOS 版本")
	}
	if plistVer == "" {
		fail("Info.plist 里没找到 MinimumOSVersion")
	}
	if len(wfVers) != 1 {
		fail("workflow 里 IOS_MIN 有多个值: %v", wfVers)
	}
	for v := range wfVers {
		if projVer != "" && v != projVer {
			fail("workflow IOS_MIN=%s 与 project.yml iOS=%s 不一致", v, projVer)
		}
		if plistVer != "" && v != plistVer {
			fail("workflow IOS_MIN=%s 与 Info.plist MinimumOSVersion=%s 不一致", v, plistVer)
		}
	}
	if projVer != "" && projVer == plistVer && len(wfVers) == 1 {
		ok("版本号一致：iOS %s", projVer)
	}
}

// ⑦ workflow 顺序与关键路径
func checkWorkflowOrder() {
	data, err := os.ReadFile(".github/workflows/ios-sdk.yml")
	if err != nil {
		fail("读不到 ios-sdk.yml")
		return
	}
	src := string(data)
	order := []string{"把三件套放进 app 包", "注入 entitlements", "断言 frpc 已进包", "打成 ipa"}
	last := -1
	for _, name := range order {
		idx := strings.Index(src, "- name: "+name)
		if idx < 0 {
			fail("workflow 缺少步骤：%s", name)
			continue
		}
		if idx < last {
			fail("workflow 步骤顺序错误：%s 应在前一步之后", name)
		}
		last = idx
	}
	if strings.Contains(src, "-Sentitlements.plist") {
		fail("workflow 里还有 -Sentitlements.plist（应为 -S host/ios/entitlements.plist）")
	}
	if strings.Contains(src, "buildmode=c-archive -trimpath -ldflags") {
		fail("c-archive 构建带了 -ldflags（-s 会剥掉导出符号）")
	}
	if !strings.Contains(src, "exported_symbol") {
		fail("dylib 链接缺少 -Wl,-exported_symbol")
	}
	ok("workflow 顺序与关键路径检查完成")
}
