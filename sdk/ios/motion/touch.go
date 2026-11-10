// 触摸与滑动：按下 / 移动 / 抬起 三段式，以及两种滑动轨迹。
//
// 【为什么是"手指标识 + 三段式"而不是只给一个 Tap】
// 很多场景要的是"按住不放、边移动边观察"（拖拽排序、长按菜单、双指缩放）；
// 只暴露 Tap/Swipe 会把这类操作逼回原生层，脚本没法组合。
//
// 【实现方式】按下/移动/抬起都走 TouchFrame（进程内输入闸门 + 坐标换算），
// 不另开一条原生通道 —— 两套手指状态并存迟早会互相踩。
package motion

import (
	"context"
	"math"
	"math/rand"
	"sync"
	"time"

	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/types"
)


// randInt 返回 [0,n) 的随机数（只用于抖动，不做密码学用途）。
func randInt(n int) int {
	if n <= 0 {
		return 0
	}
	return rand.Intn(n)
}

// fingerState 记录当前按下的手指（fingerID → 归一化坐标）。
// AutoGo 的按下/移动/抬起是三条独立调用，必须自己记状态才能算出一帧完整的触点表。
var (
	fingerMu    sync.Mutex
	fingerState = map[int]types.Contact{}
)

// displaySize 缓存屏幕物理尺寸，用来把 AutoGo 的像素坐标换算成我们的归一化坐标。
var (
	displayOnce  sync.Once
	displayW     int
	displayH     int
)

func screenSize() (int, int) {
	displayOnce.Do(func() {
		var d types.Display
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := native.Call(ctx, "device.display", nil, &d); err == nil {
			displayW, displayH = d.Width, d.Height
		}
	})
	if displayW <= 0 || displayH <= 0 {
		return 1, 1
	}
	return displayW, displayH
}

// norm 把像素坐标换成 0..1；越界夹住，避免原生侧拒绝整帧。
func norm(x, y int) (float64, float64) {
	w, h := screenSize()
	nx := float64(x) / float64(w)
	ny := float64(y) / float64(h)
	return math.Max(0, math.Min(1, nx)), math.Max(0, math.Min(1, ny))
}

// pushFrame 把当前手指状态作为一帧发给原生。
func pushFrame() {
	fingerMu.Lock()
	contacts := make([]types.Contact, 0, len(fingerState))
	for _, c := range fingerState {
		contacts = append(contacts, c)
	}
	fingerMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = TouchFrame(ctx, contacts)
}

func setFinger(fingerID, x, y int) {
	nx, ny := norm(x, y)
	fingerMu.Lock()
	fingerState[fingerID] = types.Contact{ID: fingerID, Point: types.Point{X: nx, Y: ny}}
	fingerMu.Unlock()
}

func clearFinger(fingerID int) {
	fingerMu.Lock()
	delete(fingerState, fingerID)
	fingerMu.Unlock()
}

// TouchDown 模拟触摸按下。
func TouchDown(x, y, fingerID int) {
	setFinger(fingerID, x, y)
	pushFrame()
}

// TouchMove 模拟触摸移动。
func TouchMove(x, y, fingerID int) {
	setFinger(fingerID, x, y)
	pushFrame()
}

// TouchUp 模拟触摸抬起。
func TouchUp(x, y, fingerID int) {
	setFinger(fingerID, x, y)
	pushFrame()
	clearFinger(fingerID)
	pushFrame()
}

// Click 单击：按下 → 短随机间隔 → 抬起。
// 间隔取 40~120ms：太快会被 App 当成误触，太慢就变成长按。
func Click(x, y, fingerID int) {
	TouchDown(x, y, fingerID)
	time.Sleep(time.Duration(40+randInt(80)) * time.Millisecond)
	TouchUp(x, y, fingerID)
}

// LongClick 长按 duration 毫秒。
func LongClick(x, y, duration, fingerID int) {
	if duration <= 0 {
		duration = 600
	}
	TouchDown(x, y, fingerID)
	time.Sleep(time.Duration(duration) * time.Millisecond)
	TouchUp(x, y, fingerID)
}

// Swipe 直线滑动：按时间插值成若干帧。
func Swipe(x1, y1, x2, y2, duration, fingerID int) {
	swipeLine(x1, y1, x2, y2, duration, fingerID, false)
}

// Swipe2 带弧度的滑动：轨迹更接近真人（二次贝塞尔，控制点在中点法线方向偏移）。
func Swipe2(x1, y1, x2, y2, duration, fingerID int) {
	swipeLine(x1, y1, x2, y2, duration, fingerID, true)
}

func swipeLine(x1, y1, x2, y2, duration, fingerID int, curved bool) {
	if duration <= 0 {
		duration = 400
	}
	screenW, screenH := screenSize()
	stepMs := 8 + randInt(8) // 8~15ms 一帧，和真机采样率同量级
	frames := duration / stepMs
	if frames < 2 {
		frames = 2
	}
	// 控制点：中点沿法线偏移 8%~18% 的位移长度，弯曲方向随机。
	cx, cy := float64(x1+x2)/2, float64(y1+y2)/2
	dx, dy := float64(x2-x1), float64(y2-y1)
	length := math.Hypot(dx, dy)
	if curved && length > 1 && length < math.Hypot(float64(screenW), float64(screenH)) {
		offset := length * (0.08 + float64(randInt(10))/100)
		if randInt(2) == 0 {
			offset = -offset
		}
		cx += -dy / length * offset
		cy += dx / length * offset
	}
	TouchDown(x1, y1, fingerID)
	for i := 1; i <= frames; i++ {
		t := float64(i) / float64(frames)
		var px, py float64
		if curved {
			// 二次贝塞尔：B(t) = (1-t)^2*P0 + 2(1-t)t*P1 + t^2*P2
			u := 1 - t
			px = u*u*float64(x1) + 2*u*t*cx + t*t*float64(x2)
			py = u*u*float64(y1) + 2*u*t*cy + t*t*float64(y2)
		} else {
			px = float64(x1) + dx*t
			py = float64(y1) + dy*t
		}
		TouchMove(int(px), int(py), fingerID)
		time.Sleep(time.Duration(stepMs) * time.Millisecond)
	}
	TouchUp(x2, y2, fingerID)
}
