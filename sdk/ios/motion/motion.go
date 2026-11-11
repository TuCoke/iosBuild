// Package motion 提供 iOS 触摸动作。坐标为当前显示方向的归一化坐标。
package motion

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"autogov1/v2/sdk/ios/internal/inputstate"
	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/types"
)

// 同一进程只有一个系统触摸设备，所有调用者共用状态与串行闸门。
var rotation atomic.Int32

func init() { rotation.Store(-1) }

// 默认从 FrontBoard 获取前台方向；Configure 可显式固定方向。
func currentRotation(ctx context.Context) (int, error) {
	if r := int(rotation.Load()); r >= 0 {
		return r, nil
	}
	var display types.Display
	if err := native.Call(ctx, "device.display", nil, &display); err != nil {
		return 0, err
	}
	return display.Rotation, nil
}

func acquire(ctx context.Context) error { return inputstate.Acquire(ctx) }
func release()                          { inputstate.Release() }

// Configure 显式覆盖屏幕方向和事件来源 ID；senderID=0 恢复样本默认常量，它不代表真实硬件身份。
func Configure(ctx context.Context, quarterTurns int, senderID uint64) error {
	if quarterTurns < 0 || quarterTurns > 3 {
		return types.ErrInvalid
	}
	if err := acquire(ctx); err != nil {
		return err
	}
	defer release()
	if err := native.Call(ctx, "motion.configure", map[string]any{"senderId": senderID}, nil); err != nil {
		return err
	}
	rotation.Store(int32(quarterTurns))
	return nil
}

func validate(contacts []types.Contact) error {
	if len(contacts) > 10 {
		return fmt.Errorf("%w: 最多十指", types.ErrInvalid)
	}
	seen := uint16(0)
	for _, c := range contacts {
		if c.ID < 0 || c.ID >= 10 || !c.Point.Valid() {
			return fmt.Errorf("%w: 手指编号或坐标无效", types.ErrInvalid)
		}
		bit := uint16(1) << c.ID
		if seen&bit != 0 {
			return fmt.Errorf("%w: 手指编号重复", types.ErrInvalid)
		}
		seen |= bit
	}
	return nil
}
func frame(ctx context.Context, contacts []types.Contact, requireIdle ...bool) error {
	if err := validate(contacts); err != nil {
		return err
	}
	turn, err := currentRotation(ctx)
	if err != nil {
		return err
	}
	return frameAtRotation(ctx, contacts, turn, requireIdle...)
}
func frameAtRotation(ctx context.Context, contacts []types.Contact, turn int, requireIdle ...bool) error {
	natural := make([]types.Contact, len(contacts))
	for i, c := range contacts {
		c.Point = c.Point.Natural(turn)
		natural[i] = c
	}
	args := map[string]any{"contacts": natural}
	if len(requireIdle) > 0 {
		args["requireIdle"] = requireIdle[0]
	}
	return native.Call(ctx, "motion.frame", args, nil)
}

// TouchFrame 提交所有按下手指的完整快照，空列表释放当前全部手指。
func TouchFrame(ctx context.Context, contacts []types.Contact) error {
	if err := acquire(ctx); err != nil {
		return err
	}
	defer release()
	return frame(ctx, contacts)
}
func Reset(ctx context.Context) error { return TouchFrame(ctx, nil) }

func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Gesture 的帧时间相对于开始时间。手势结束或取消后会独立尝试抬手，不用已取消的 context 清理。
func Gesture(ctx context.Context, frames []types.Frame) (err error) {
	if len(frames) == 0 || len(frames) > 16000 {
		return types.ErrInvalid
	}
	previous := -1
	for i, f := range frames {
		if f.AtMS < 0 || f.AtMS > 120000 || (i > 0 && f.AtMS <= previous) || (i == 0 && f.AtMS != 0) {
			return fmt.Errorf("%w: 帧时间需从零开始严格递增，最长两分钟", types.ErrInvalid)
		}
		previous = f.AtMS
		if e := validate(f.Contacts); e != nil {
			return e
		}
	}
	if err = acquire(ctx); err != nil {
		return err
	}
	defer release()
	turn, e := currentRotation(ctx)
	if e != nil {
		return e
	}
	started := false
	defer func() {
		if started {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if e := frameAtRotation(cleanup, nil, turn); e != nil {
				err = errors.Join(err, &types.Error{Op: "motion.cleanup", Kind: types.ErrUnknown, Detail: e.Error()})
			}
		}
		if err != nil && started && !errors.Is(err, types.ErrUnknown) {
			err = &types.Error{Op: "motion.gesture", Kind: types.ErrUnknown, Detail: "手势部分已提交: " + err.Error()}
		}
	}()
	begin := time.Now()
	for _, f := range frames {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = wait(ctx, time.Until(begin.Add(time.Duration(f.AtMS)*time.Millisecond))); err != nil {
			return err
		}
		if err = frameAtRotation(ctx, f.Contacts, turn, !started); err != nil {
			if errors.Is(err, types.ErrUnknown) {
				started = true
			}
			return err
		}
		started = true
	}
	return nil
}
func Tap(ctx context.Context, x, y float64) error { return LongPress(ctx, x, y, 60*time.Millisecond) }
func LongPress(ctx context.Context, x, y float64, d time.Duration) error {
	if d < time.Millisecond || d > 120*time.Second {
		return types.ErrInvalid
	}
	return Gesture(ctx, []types.Frame{{AtMS: 0, Contacts: []types.Contact{{ID: 0, Point: types.Point{X: x, Y: y}}}}, {AtMS: int(d / time.Millisecond)}})
}

// SwipePath 是"归一化坐标 + 可取消"的高精度滑动；像素坐标的简版 Swipe 在 touch.go。
func SwipePath(ctx context.Context, x1, y1, x2, y2 float64, d time.Duration) error {
	if d < time.Millisecond || d > 119*time.Second {
		return types.ErrInvalid
	}
	ms := int(d / time.Millisecond)
	steps := ms / 16
	if steps < 1 {
		steps = 1
	}
	frames := make([]types.Frame, 0, steps+2)
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		t = t * t * (3 - 2*t)
		frames = append(frames, types.Frame{AtMS: i * ms / steps, Contacts: []types.Contact{{ID: 0, Point: types.Point{X: x1 + (x2-x1)*t, Y: y1 + (y2-y1)*t}}}})
	}
	frames = append(frames, types.Frame{AtMS: ms + 1})
	return Gesture(ctx, frames)
}

// Pinch 指定两根手指各自的起止点，不假定横竖屏或屏幕长宽比。
func Pinch(ctx context.Context, a0, b0, a1, b1 types.Point, d time.Duration) error {
	if d < time.Millisecond || d > 119*time.Second {
		return types.ErrInvalid
	}
	ms := int(d / time.Millisecond)
	steps := int(math.Max(1, float64(ms/16)))
	frames := make([]types.Frame, 0, steps+2)
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		frames = append(frames, types.Frame{AtMS: i * ms / steps, Contacts: []types.Contact{{ID: 0, Point: types.Point{X: a0.X + (a1.X-a0.X)*t, Y: a0.Y + (a1.Y-a0.Y)*t}}, {ID: 1, Point: types.Point{X: b0.X + (b1.X-b0.X)*t, Y: b0.Y + (b1.Y-b0.Y)*t}}}})
	}
	frames = append(frames, types.Frame{AtMS: ms + 1})
	return Gesture(ctx, frames)
}
