package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"autogov1/v2/sdk/ios/script/worker"
)

// 真正从文件和 ZIP 运行入口，并检查宿主结果，不能仅检查构建是否成功。
func TestFileAndZipExecution(t *testing.T) {
	original, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(original) })
	for _, extension := range []string{".go", ".zip"} {
		dir := t.TempDir()
		filename := filepath.Join(dir, "task"+extension)
		source := `package main;import "autogov1/v2/sdk/ios/script/api";func main(){api.Log("running");api.SetResult(api.ParamString("name"))}`
		raw := []byte(source)
		if extension == ".zip" {
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			f, _ := z.Create("main.go")
			_, _ = f.Write(raw)
			_ = z.Close()
			raw = b.Bytes()
		}
		_ = os.WriteFile(filename, raw, 0600)
		parameters := filepath.Join(dir, "params.json")
		_ = os.WriteFile(parameters, []byte(`{"name":"ios"}`), 0600)
		resultPath := filepath.Join(dir, "result.json")
		var stdout, stderr bytes.Buffer
		code := run([]string{"--params", parameters, "--result", resultPath, filename}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("%s: %d %s", extension, code, stderr.String())
		}
		body, err := os.ReadFile(resultPath)
		if err != nil {
			t.Fatal(err)
		}
		var reply worker.Reply
		if err = json.Unmarshal(body, &reply); err != nil || !reply.OK || reply.Value != "ios" || len(reply.Logs) != 1 {
			t.Fatalf("结果错误: %+v %v", reply, err)
		}
	}
}
