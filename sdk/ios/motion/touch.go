// 像素动作复用原生逐指状态；坐标、方向和 IOHID 状态统一在原生层处理。
package motion

import (
	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/types"
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"time"
)

func randInt(n int) int {
	if n <= 0 {
		return 0
	}
	return rand.Intn(n)
}
func pointer(ctx context.Context, x, y, finger int, phase string, requireIdle ...bool) error {
	if finger < 0 || finger > 9 || x < 0 || y < 0 || (phase != "down" && phase != "move" && phase != "up") {
		return types.ErrInvalid
	}
	args := map[string]any{"x": x, "y": y, "id": finger, "phase": phase}
	if len(requireIdle) > 0 {
		args["requireIdle"] = requireIdle[0]
	}
	return native.Call(ctx, "motion.pointer", args, nil)
}

// TouchContext 支持逐指按下、移动、抬起；完整动作请使用 ClickContext 或 SwipeContext。
func TouchContext(ctx context.Context, x, y, finger int, phase string) error {
	if err := acquire(ctx); err != nil {
		return err
	}
	defer release()
	return pointer(ctx, x, y, finger, phase)
}
func TouchDown(x, y, finger int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = TouchContext(ctx, x, y, finger, "down")
}
func TouchMove(x, y, finger int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = TouchContext(ctx, x, y, finger, "move")
}
func TouchUp(x, y, finger int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = TouchContext(ctx, x, y, finger, "up")
}

// ClickContext 持有整个动作的输入闸门；取消后仅抬起自己的手指。
func ClickContext(ctx context.Context, x, y, finger, duration int) (err error) {
	if duration < 1 || duration > 120000 {
		return types.ErrInvalid
	}
	if err = acquire(ctx); err != nil {
		return err
	}
	defer release()
	if err = pointer(ctx, x, y, finger, "down", true); err != nil {
		return cleanupUnknownDown(x, y, finger, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e := pointer(cleanup, x, y, finger, "up"); e != nil {
			err = errors.Join(err, &types.Error{Op: "motion.cleanup", Kind: types.ErrUnknown, Detail: e.Error()})
		}
	}()
	if err = wait(ctx, time.Duration(duration)*time.Millisecond); err != nil {
		return &types.Error{Op: "motion.click", Kind: types.ErrUnknown, Detail: err.Error()}
	}
	return nil
}
func Click(x, y, finger int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = ClickContext(ctx, x, y, finger, 10+randInt(11))
}
func LongClick(x, y, duration, finger int) {
	ctx, cancel := context.WithTimeout(context.Background(), 121*time.Second)
	defer cancel()
	_ = ClickContext(ctx, x, y, finger, duration)
}
func Swipe(x1, y1, x2, y2, duration, finger int) {
	ctx, cancel := context.WithTimeout(context.Background(), 121*time.Second)
	defer cancel()
	_ = SwipeContext(ctx, x1, y1, x2, y2, duration, finger, false)
}
func Swipe2(x1, y1, x2, y2, duration, finger int) {
	ctx, cancel := context.WithTimeout(context.Background(), 121*time.Second)
	defer cancel()
	_ = SwipeContext(ctx, x1, y1, x2, y2, duration, finger, true)
}

// trajectory 计算二次贝塞尔轨迹上的点（纯函数，可单测）。
//
// 抽出来的理由：轨迹生成是整个滑动里唯一需要"算对"的部分，
// 之前它和 TouchDown/Move/Up 混在一起，只能靠真机看效果，没法测。
func trajectory(x1, y1, x2, y2 int, frames int, cx, cy float64) [][2]int {
	if frames < 2 {
		frames = 2
	}
	pts := make([][2]int, 0, frames)
	for i := 1; i <= frames; i++ {
		t := float64(i) / float64(frames)
		u := 1 - t
		px := u*u*float64(x1) + 2*u*t*cx + t*t*float64(x2)
		py := u*u*float64(y1) + 2*u*t*cy + t*t*float64(y2)
		pts = append(pts, [2]int{int(px), int(py)})
	}
	return pts
}

// SwipeContext 与样本采用相同的 10 ms 分段与绝对时间调度，避免逐帧 sleep 累积耗时。
func SwipeContext(ctx context.Context, x1, y1, x2, y2, duration, finger int, curved bool) (err error) {
	if duration < 1 || duration > 120000 || finger < 0 || finger > 9 || x1 < 0 || y1 < 0 || x2 < 0 || y2 < 0 {
		return types.ErrInvalid
	}
	if err = acquire(ctx); err != nil {
		return err
	}
	defer release()
	if err = pointer(ctx, x1, y1, finger, "down", true); err != nil {
		return cleanupUnknownDown(x1, y1, finger, err)
	}
	lastX, lastY := x1, y1
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e := pointer(cleanup, lastX, lastY, finger, "up"); e != nil {
			err = errors.Join(err, &types.Error{Op: "motion.cleanup", Kind: types.ErrUnknown, Detail: e.Error()})
		}
	}()
	frames := duration / 10
	if frames < 1 {
		frames = 1
	}
	cx, cy := float64(x1+x2)/2, float64(y1+y2)/2
	if curved {
		dx, dy := float64(x2-x1), float64(y2-y1)
		offset := (float64(randInt(1000))/1000 - 0.5) * 0.4 * math.Min(math.Hypot(dx, dy), 200)
		angle := math.Atan2(dy, dx) + math.Pi/2
		cx += math.Cos(angle) * offset
		cy += math.Sin(angle) * offset
	}
	points := trajectory(x1, y1, x2, y2, frames, cx, cy)
	start := time.Now()
	for i, p := range points {
		if err = wait(ctx, time.Until(start.Add(time.Duration(i+1)*time.Duration(duration)*time.Millisecond/time.Duration(len(points))))); err != nil {
			return &types.Error{Op: "motion.swipe", Kind: types.ErrUnknown, Detail: err.Error()}
		}
		if err = pointer(ctx, p[0], p[1], finger, "move"); err != nil {
			return fmt.Errorf("滑动部分已提交: %w", errors.Join(types.ErrUnknown, err))
		}
		lastX, lastY = p[0], p[1]
	}
	return nil
}

// 首次按下若结果未知也必须清理；确定未执行的 Busy/参数错误不能抬掉其他调用者的手指。
func cleanupUnknownDown(x, y, finger int, err error) error {
	if !errors.Is(err, types.ErrUnknown) {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return errors.Join(err, pointer(ctx, x, y, finger, "up"))
}
