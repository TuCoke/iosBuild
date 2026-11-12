package transport

import (
	"context"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// 安卓既有 query 鉴权与 iOS 每连接 Header 鉴权同时使用，不能覆盖彼此的凭据。
func TestScopedIOSAuthDoesNotChangeAndroidConnection(t *testing.T) {
	SetToken("android-test-token")
	defer SetToken("")
	var seen sync.Map
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/android":
			if r.URL.Query().Get("token") != "android-test-token" || r.Header.Get("Authorization") != "" {
				t.Error("安卓凭据被 iOS 改写")
			}
		case "/ios":
			if r.Header.Get("Authorization") != "Bearer ios-test-token" || r.URL.RawQuery != "" {
				t.Error("iOS 连接借用了安卓全局 token")
			}
		default:
			t.Error("未知连接")
		}
		seen.Store(r.URL.Path, true)
		ws, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e == nil {
			ws.Close()
		}
	}))
	defer server.Close()
	base := strings.Replace(server.URL, "http", "ws", 1)
	var wg sync.WaitGroup
	for _, platform := range []string{"android", "ios"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			var c *Conn
			var e error
			if name == "android" {
				c, e = Dial(base + "/android")
			} else {
				c, e = DialAuthenticated(context.Background(), base+"/ios", "ios-test-token")
			}
			if e != nil {
				t.Error(e)
				return
			}
			c.Close()
		}(platform)
	}
	wg.Wait()
	if _, ok := seen.Load("/ios"); !ok {
		t.Error("iOS 未接入")
	}
	if _, ok := seen.Load("/android"); !ok {
		t.Error("安卓未接入")
	}
}
