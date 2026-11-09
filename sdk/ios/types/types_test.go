package types

import (
	"math"
	"testing"
)

func TestPointNaturalMatchesDisplayedCorners(t *testing.T) {
	// 以显示方向左上角为基准，四个方向必须映射到四个不同的自然方向角点。
	want := []Point{{0, 0}, {0, 1}, {1, 1}, {1, 0}}
	for rotation, expected := range want {
		if got := (Point{0, 0}).Natural(rotation); got != expected {
			t.Fatalf("方向 %d: 得到 %+v，期望 %+v", rotation, got, expected)
		}
		if got := (Point{.5, .5}).Natural(rotation); got != (Point{.5, .5}) {
			t.Fatalf("旋转不应改变中心点: %+v", got)
		}
	}
}

func TestPointRejectsNonFiniteAndOutsideCoordinates(t *testing.T) {
	for _, p := range []Point{{math.NaN(), 0}, {0, math.Inf(1)}, {-0.01, 0}, {0, 1.01}} {
		if p.Valid() {
			t.Fatalf("接受了无效坐标: %+v", p)
		}
	}
	for _, p := range []Point{{0, 0}, {1, 1}, {.25, .75}} {
		if !p.Valid() {
			t.Fatalf("拒绝了合法坐标: %+v", p)
		}
	}
}

func TestDisplaySeparatesNativeAndRotatedSize(t *testing.T) {
	d := Display{Width: 1170, Height: 2532, Scale: 3}
	if w, h := d.Size(1); w != 2532 || h != 1170 {
		t.Fatalf("横屏尺寸错误: %d x %d", w, h)
	}
	if w, h := d.Size(2); w != 1170 || h != 2532 {
		t.Fatalf("倒置竖屏尺寸错误: %d x %d", w, h)
	}
}
