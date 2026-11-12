package runtime

import (
	"testing"
	"time"
)

func TestServerClockIgnoresPhoneWallTime(t *testing.T) {
	for _, skew := range []time.Duration{-273 * time.Second, 273 * time.Second} {
		server := time.Now().Add(skew).UnixMilli()
		c := serverClock{serverMS: server, anchor: time.Now().Add(-2 * time.Second)}
		budget, ok := c.budget(server + 5000)
		if !ok || budget < 2900*time.Millisecond || budget > 3100*time.Millisecond {
			t.Fatalf("skew=%s budget=%s ok=%v", skew, budget, ok)
		}
	}
}
func TestServerClockLongConnectionAndExpired(t *testing.T) {
	c := serverClock{serverMS: 1000000, anchor: time.Now().Add(-time.Hour)}
	if budget, ok := c.budget(1000000 + 3600000 + 5000); !ok || budget < 4900*time.Millisecond || budget > 5100*time.Millisecond {
		t.Fatalf("长连接预算错误: %s %v", budget, ok)
	}
	if _, ok := c.budget(1000000 + 3600000 - 1); ok {
		t.Fatal("过期命令被接受")
	}
	if _, ok := (serverClock{}).budget(123); ok {
		t.Fatal("缺少服务端时钟不得执行有期限命令")
	}
}
