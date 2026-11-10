package utils

import (
	"testing"
	"time"
)

func TestConversions(t *testing.T) {
	if I2s(42) != "42" {
		t.Fatalf("I2s = %q", I2s(42))
	}
	if S2i("42") != 42 {
		t.Fatalf("S2i = %d", S2i("42"))
	}
	// 非数字应回退 0，而不是 panic
	if S2i("abc") != 0 {
		t.Fatalf("S2i(abc) = %d", S2i("abc"))
	}
	if F2s(1.5) != "1.5" {
		t.Fatalf("F2s = %q", F2s(1.5))
	}
	if S2f("1.5") != 1.5 {
		t.Fatalf("S2f = %v", S2f("1.5"))
	}
	if S2f("x") != 0 {
		t.Fatalf("S2f(x) = %v", S2f("x"))
	}
	if B2s(true) != "true" || B2s(false) != "false" {
		t.Fatal("B2s 不对")
	}
	if !S2b("true") || S2b("false") {
		t.Fatal("S2b 不对")
	}
	// strconv.ParseBool 的语义："1" / "t" / "TRUE" 都算 true，未知串算 false
	if !S2b("1") || !S2b("TRUE") {
		t.Fatal(`S2b 应接受 "1" 与 "TRUE"`)
	}
	if S2b("yes") {
		t.Fatal(`S2b("yes") 应为 false（解析失败回退零值）`)
	}
}

// Random 是闭区间：跑了足够多次必须能碰到两端。
func TestRandomInclusiveRange(t *testing.T) {
	seenMin, seenMax := false, false
	for i := 0; i < 2000; i++ {
		v := Random(1, 3)
		if v < 1 || v > 3 {
			t.Fatalf("Random 越界: %d", v)
		}
		if v == 1 {
			seenMin = true
		}
		if v == 3 {
			seenMax = true
		}
	}
	if !seenMin || !seenMax {
		t.Fatalf("闭区间未覆盖两端: min=%v max=%v", seenMin, seenMax)
	}
	if got := Random(5, 5); got != 5 {
		t.Fatalf("min==max 应返回该值，实际 %d", got)
	}
}

func TestSleep(t *testing.T) {
	start := time.Now()
	Sleep(60)
	elapsed := time.Since(start)
	if elapsed < 50*time.Millisecond {
		t.Fatalf("Sleep(60) 只花了 %v", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Sleep(60) 花了 %v，太慢", elapsed)
	}
	// 0 / 负数不该阻塞
	Sleep(0)
	Sleep(-5)
}
