package script

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"autogov1/v2/sdk/ios/files"
)

func TestRemoteCancellationAndInvalidDigest(t *testing.T) {
	files.SetRoot(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := New(Options{}).RunRemote(ctx, RemoteOptions{URL: server.URL, Timeout: time.Hour})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("下载没有继承取消: %v", err)
	}
	_, err = New(Options{}).RunRemote(context.Background(), RemoteOptions{URL: server.URL, SHA256: "a"})
	if err == nil || !strings.Contains(err.Error(), "SHA256") {
		t.Fatalf("错误摘要未提前拒绝: %v", err)
	}
}
func TestFailedRemoteDoesNotOverwriteWorkingCacheOrReplay(t *testing.T) {
	files.SetRoot(t.TempDir())
	old := `package main;func main(){}`
	CacheSource("daily", old)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`package main;func main(){panic("failed")}`))
	}))
	defer server.Close()
	_, err := New(Options{}).RunRemote(context.Background(), RemoteOptions{URL: server.URL, CacheName: "daily"})
	if err == nil {
		t.Fatal("失败的新脚本被回退重跑掩盖")
	}
	if CachedSource("daily") != old {
		t.Fatal("失败版本覆盖了有效缓存")
	}
	for _, name := range []string{"../escape", "/absolute", "a\\b", "a:b"} {
		if _, err := cacheFilename(name); err == nil {
			t.Fatal("缓存接受了路径", name)
		}
	}
}
