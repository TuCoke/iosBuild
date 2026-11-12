package gesture

import (
	"math"
	"os"
	"testing"
)

// TestInnerOffsetKeepsInside 是本轮改动最要紧的约束:落点可以偏,但不能偏出控件。
// 偏出去的后果是"偶尔点不中",而且控件越小越频繁。
func TestInnerOffsetKeepsInside(t *testing.T) {
	for _, size := range []int{24, 30, 48, 60, 100, 300, 1080} {
		limit := size / 5
		if limit > 12 {
			limit = 12
		}
		for i := 0; i < 1000; i++ {
			off := InnerOffset(size)
			if off > limit || off < -limit {
				t.Fatalf("size=%d 偏移 %d 超过上限 ±%d", size, off, limit)
			}
			// 落点 = 中心 + 偏移,必须仍在 [0,size) 内
			center := size / 2
			if center+off < 0 || center+off >= size {
				t.Fatalf("size=%d 偏移 %d 让落点跑出控件", size, off)
			}
		}
	}
}

// TestInnerOffsetTinyEdge 短于 24px 的边不偏移:那个尺度上不拿命中率换拟人度。
func TestInnerOffsetTinyEdge(t *testing.T) {
	for _, size := range []int{-10, 0, 1, 23} {
		for i := 0; i < 100; i++ {
			if got := InnerOffset(size); got != 0 {
				t.Fatalf("size=%d 应返回 0 偏移,却是 %d", size, got)
			}
		}
	}
}

// TestInnerOffsetVaries 偏移必须真的在变:恒定值等于没加随机化。
func TestInnerOffsetVaries(t *testing.T) {
	seen := map[int]bool{}
	for i := 0; i < 200; i++ {
		seen[InnerOffset(200)] = true
	}
	if len(seen) < 3 {
		t.Fatalf("落点偏移几乎没有变化(只出现 %d 种值),随机化没生效", len(seen))
	}
}

// TestHoldRange 按压时长落在 50~120ms。
func TestHoldRange(t *testing.T) {
	seen := map[int]bool{}
	for i := 0; i < 500; i++ {
		v := Hold()
		if v < 50 || v > 119 {
			t.Fatalf("按压时长 %d 超出 50~120ms", v)
		}
		seen[v] = true
	}
	if len(seen) < 5 {
		t.Fatalf("按压时长几乎没有变化(只出现 %d 种),随机化没生效", len(seen))
	}
}

// TestPreDelayRange 落指前停顿落在 30~120ms。
func TestPreDelayRange(t *testing.T) {
	seen := map[int64]bool{}
	for i := 0; i < 500; i++ {
		d := PreDelay()
		ms := int64(d / 1e6)
		if ms < 30 || ms > 119 {
			t.Fatalf("落指前停顿 %dms 超出 30~120ms", ms)
		}
		seen[ms] = true
	}
	if len(seen) < 5 {
		t.Fatalf("落指前停顿几乎没有变化(只出现 %d 种)", len(seen))
	}
}

// TestJitterDurWithin8Percent 时长抖动不超过 ±8%。
func TestJitterDurWithin8Percent(t *testing.T) {
	for _, dur := range []int{150, 200, 300, 600, 1200} {
		limit := float64(dur)/12 + 1 // 允许 1ms 的取整误差
		seen := map[int]bool{}
		for i := 0; i < 500; i++ {
			got := JitterDur(dur)
			if diff := math.Abs(float64(got - dur)); diff > limit {
				t.Fatalf("时长 %d 抖动到 %d,超出 ±8%%", dur, got)
			}
			seen[got] = true
		}
		if len(seen) < 3 {
			t.Fatalf("时长 %d 的抖动几乎没有变化", dur)
		}
	}
	// 非法/零时长原样返回,避免上层拿到负数 sleep
	if JitterDur(0) != 0 || JitterDur(-5) != -5 {
		t.Fatal("零/负时长应原样返回")
	}
}

// FrameMs 的取值必须落在面板可能的范围内，并且能被环境变量覆盖。
func TestFrameMs(t *testing.T) {
	os.Unsetenv("AUTOGO_FRAME_MS")
	if got := FrameMs(); got != 8 {
		t.Fatalf("默认应为 8ms，实际 %d", got)
	}
	os.Setenv("AUTOGO_FRAME_MS", "16")
	defer os.Unsetenv("AUTOGO_FRAME_MS")
	if got := FrameMs(); got != 16 {
		t.Fatalf("应读环境变量 16，实际 %d", got)
	}
	os.Setenv("AUTOGO_FRAME_MS", "1")
	if got := FrameMs(); got != 6 {
		t.Fatalf("过小应夹到 6，实际 %d", got)
	}
	os.Setenv("AUTOGO_FRAME_MS", "999")
	if got := FrameMs(); got != 20 {
		t.Fatalf("过大应夹到 20，实际 %d", got)
	}
}

// Wander 的形状约束：两端收敛到 0、中段在真人观测区间内、长度等于帧数。
func TestWanderShape(t *testing.T) {
	const steps = 150
	w := Wander(steps, 0.25)
	if len(w) != steps {
		t.Fatalf("长度应为 %d，实际 %d", steps, len(w))
	}
	if math.Abs(w[0]) > 0.004 {
		t.Errorf("起手应贴近直线，实际 %.4f", w[0])
	}
	if math.Abs(w[steps-1]) > 0.004 {
		t.Errorf("抬手应贴近直线，实际 %.4f", w[steps-1])
	}
	peak := 0.0
	for _, v := range w {
		if math.Abs(v) > peak {
			peak = math.Abs(v)
		}
	}
	// 真人数据：偏离直线 0.03~0.22 屏；摆动只贡献其中横向的一部分，取 0.005~0.021
	if peak < 0.004 || peak > 0.022 {
		t.Errorf("摆动峰值应在 0.004~0.022 屏，实际 %.4f", peak)
	}
	// 零距离时不应产生位移（长按退化分支依赖这个）
	for _, v := range Wander(steps, 0) {
		if math.Abs(v) > 0.022 {
			t.Fatalf("零距离摆动过大: %.4f", v)
		}
	}
}

func TestFrameValidationAndRotation(t *testing.T) {
	good := []Frame{{AtMS: 0, Contacts: []Contact{{ID: 0, X: .2, Y: .3}, {ID: 9, X: .8, Y: .7}}}, {AtMS: 100, Contacts: []Contact{}}}
	if err := ValidateFrames(good); err != nil {
		t.Fatal(err)
	}
	cases := [][]Frame{nil, {{AtMS: 1, Contacts: good[0].Contacts}}, {{AtMS: 0, Contacts: []Contact{{ID: 1, X: .5, Y: .5}, {ID: 1, X: .2, Y: .2}}}}, {{AtMS: 0, Contacts: []Contact{{ID: 10, X: .5, Y: .5}}}}, {{AtMS: 0, Contacts: []Contact{{ID: 0, X: math.NaN(), Y: .5}}}}, {{AtMS: 0, Contacts: good[0].Contacts}, {AtMS: 120001}}}
	for i, f := range cases {
		if ValidateFrames(f) == nil {
			t.Fatalf("非法轨迹%d被接受", i)
		}
	}
	for r := 0; r < 4; r++ {
		x, y := ToNatural(.2, .7, r)
		a, b := ToNatural(x, y, (4-r)&3)
		if math.Abs(a-.2) > 1e-9 || math.Abs(b-.7) > 1e-9 {
			t.Fatal("旋转往返不一致", r, a, b)
		}
	}
}
