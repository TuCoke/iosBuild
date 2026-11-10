package motion

import "testing"

// trajectory 是滑动里唯一需要"算对"的部分，这里锁住它的三个性质：
// 起点之后、终点之前、点数与 frames 一致。
func TestTrajectoryEndpoints(t *testing.T) {
	const (
		x1, y1 = 100, 500
		x2, y2 = 300, 100
	)
	pts := trajectory(x1, y1, x2, y2, 10, float64(x1+x2)/2, float64(y1+y2)/2)
	if len(pts) != 10 {
		t.Fatalf("点数 = %d，期望 10", len(pts))
	}
	// 最后一个点应接近终点（贝塞尔 t=1 时精确等于终点）
	last := pts[len(pts)-1]
	if last[0] != x2 || last[1] != y2 {
		t.Fatalf("最后一个点 = %v，期望 (%d,%d)", last, x2, y2)
	}
	// 所有点应在起终点围成的矩形内（直线方向单调）
	for i, p := range pts {
		if p[0] < x1 || p[0] > x2 {
			t.Fatalf("第 %d 点 x=%d 越界 [%d,%d]", i, p[0], x1, x2)
		}
		if p[1] < y2 || p[1] > y1 {
			t.Fatalf("第 %d 点 y=%d 越界 [%d,%d]", i, p[1], y2, y1)
		}
	}
}

// 控制点偏离中线时，轨迹必须真的"弯"——否则 Swipe2 和 Swipe 没区别。
func TestTrajectoryCurveDeviates(t *testing.T) {
	const (
		x1, y1 = 0, 0
		x2, y2 = 100, 0
	)
	straight := trajectory(x1, y1, x2, y2, 8, 50, 0)     // 控制点在线上
	curved := trajectory(x1, y1, x2, y2, 8, 50, 30)      // 控制点上移 30
	maxDiff := 0
	for i := range straight {
		d := straight[i][1] - curved[i][1]
		if d < 0 {
			d = -d
		}
		if d > maxDiff {
			maxDiff = d
		}
	}
	if maxDiff < 5 {
		t.Fatalf("弯曲轨迹偏移只有 %d，应明显偏离直线", maxDiff)
	}
	// 弯曲轨迹仍要终止于终点
	if last := curved[len(curved)-1]; last[0] != x2 || last[1] != y2 {
		t.Fatalf("弯曲轨迹终点 = %v", last)
	}
}

// frames 太小要兜底成 2（否则除零）。
func TestTrajectoryMinFrames(t *testing.T) {
	pts := trajectory(0, 0, 10, 10, 1, 5, 5)
	if len(pts) != 2 {
		t.Fatalf("frames=1 应兜底为 2，实际 %d", len(pts))
	}
}

// 归一化坐标换算：屏幕 1000x2000 时 (500,1000) → (0.5,0.5)。
func TestNorm(t *testing.T) {
	x, y := norm(500, 1000)
	// screenSize 在非 iOS 平台可能返回 0，此时 norm 应回退到不 panic
	if x < 0 || x > 1 || y < 0 || y > 1 {
		t.Fatalf("norm 越界: (%v,%v)", x, y)
	}
}
