package script

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

// 验证类型、构造器和接收者方法确实可被解释器使用，不能只检查符号名存在。
func TestSDKTypesAndConstructors(t *testing.T) {
	source := `package main
 import("autogov1/v2/sdk/ios/uiacc";"autogov1/v2/sdk/ios/ppocr";"autogov1/v2/sdk/ios/images";"autogov1/v2/sdk/ios/imgui";"autogov1/v2/sdk/ios/script/api")
 func main(){u:=uiacc.New();defer u.Release();u.Text("hello");p:=ppocr.New("v5");p.Close();r:=uiacc.Rect{Left:3};o:=ppocr.Result{Label:"ok"};point:=images.Point{X:7};v:=imgui.Vec2{X:2};api.SetResult(map[string]any{"sum":r.Left+point.X+int(v.X),"label":o.Label})}`
	result, err := New(Options{}).Run(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	got := result.Value.(map[string]any)
	if got["sum"] != float64(12) || got["label"] != "ok" {
		t.Fatal(got)
	}
}
func TestRunnerStartsFreshInterpreter(t *testing.T) {
	r := New(Options{})
	source := `package main;import "autogov1/v2/sdk/ios/script/api";var n=0;func main(){n++;api.SetResult(n)}`
	for i := 0; i < 2; i++ {
		result, err := r.Run(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		if result.Value != float64(1) {
			t.Fatalf("运行间共享了全局变量: %v", result.Value)
		}
	}
}
func TestConcurrentRunnerIsolation(t *testing.T) {
	var wg sync.WaitGroup
	for n := 0; n < 6; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprint(n)
			source := `package main;import "autogov1/v2/sdk/ios/script/api";func main(){api.Log(api.ParamString("id"));api.Sleep(10);api.SetResult(api.ParamString("id"))}`
			result, err := New(Options{Params: map[string]any{"id": id}}).Run(context.Background(), source)
			if err != nil {
				t.Error(err)
				return
			}
			if result.Value != id || strings.Join(result.Logs, "") != id {
				t.Errorf("任务结果串线: %v %v", result.Value, result.Logs)
			}
		}(n)
	}
	wg.Wait()
}
func TestTimeoutAndReuse(t *testing.T) {
	r := New(Options{Timeout: 1500 * time.Millisecond})
	_, err := r.Run(context.Background(), `package main;func main(){for {}}`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("未按时取消: %v", err)
	}
	result, err := r.Run(context.Background(), `package main;import "autogov1/v2/sdk/ios/script/api";func main(){api.SetResult("new")}`)
	if err != nil || result.Value != "new" {
		t.Fatalf("超时污染后续运行: %v %v", result, err)
	}
}
func TestProjectImportsAndEmbed(t *testing.T) {
	files := fstest.MapFS{
		"go.mod": {Data: []byte("module example.test/demo\n")},
		"main.go": {Data: []byte(`package main
 import("embed";"fmt";"io/fs";"example.test/demo/helper";"autogov1/v2/sdk/ios/script/api")
 //go:embed assets/a.txt
 var text string
 //go:embed assets/a.txt
 var raw []byte
 //go:embed assets
 var resources embed.FS
 func main(){b,err:=fs.ReadFile(resources,"assets/a.txt");if err!=nil{panic(err)};fmt.Println(helper.Value());api.SetResult(text+string(raw)+string(b)+other())}`)},
		"other.go":         {Data: []byte(`package main;func other()string{return "!"}`)},
		"helper/helper.go": {Data: []byte(`package helper;func Value()string{return "loaded"}`)},
		"assets/a.txt":     {Data: []byte("hello")},
	}
	r := New(Options{})
	result, err := r.RunProject(context.Background(), files, "main.go")
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "hellohellohello!" || strings.Join(result.Logs, "") != "loaded" {
		t.Fatalf("工程执行错误: %#v", result)
	}
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, file := range files {
		f, err := writer.Create("demo/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(file.Data); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	result, err = r.RunZip(context.Background(), buf.Bytes(), "")
	if err != nil || result.Value != "hellohellohello!" {
		t.Fatalf("ZIP 工程执行错误: %#v %v", result, err)
	}
}
func TestRejectInvalidProjects(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("../main.go")
	f.Write([]byte("package main"))
	w.Close()
	if _, err := New(Options{}).RunZip(context.Background(), buf.Bytes(), ""); err == nil {
		t.Fatal("接受了越界 ZIP")
	}
	files := fstest.MapFS{"main.go": {Data: []byte("package main\nimport _ \"embed\"\n//go:embed missing.txt\nvar value string\nfunc main(){}")}}
	if _, err := New(Options{}).RunProject(context.Background(), files, ""); err == nil {
		t.Fatal("缺失 embed 资源未报错")
	}
}

func TestSetResultReplacesPreviousObject(t *testing.T) {
	result, err := New(Options{}).Run(context.Background(), `package main;import "autogov1/v2/sdk/ios/script/api";func main(){api.SetResult(map[string]any{"old":1});api.SetResult(map[string]any{"new":2})}`)
	if err != nil {
		t.Fatal(err)
	}
	got := result.Value.(map[string]any)
	if len(got) != 1 || got["new"] != float64(2) {
		t.Fatal("结果残留上次字段", got)
	}
}
