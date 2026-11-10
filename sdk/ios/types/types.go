// Package types 定义 iOS SDK 的数据约定，不依赖 Agent 或安卓能力包。
package types

import (
	"errors"
	"fmt"
	"math"
)

var (
	ErrUnsupported = errors.New("当前环境不支持该 iOS 能力")
	ErrInvalid     = errors.New("iOS SDK 参数无效")
	ErrUnavailable = errors.New("iOS 原生服务不可用")
	ErrUnknown     = errors.New("操作可能已提交，结果需要核对")
	ErrBusy        = errors.New("iOS 资源正在使用")
	ErrTimeout     = errors.New("iOS 原生调用超时")
	ErrClosed      = errors.New("iOS 资源已关闭")
)

// Error 保留错误类别，调用方应使用 errors.Is，不能按提示文字决定是否重试。
type Error struct {
	Op     string
	Kind   error
	Detail string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %v: %s", e.Op, e.Kind, e.Detail) }
func (e *Error) Unwrap() error { return e.Kind }

// Display 的 Width/Height 是自然方向的原生像素，Rotation 是顺时针四分之一圈数。
// 旋转由调用方明确指定；不把宿主 App 的方向冒充整个系统的前台方向。
type Display struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Scale  float64 `json:"scale"`
}

func (d Display) Size(rotation int) (int, int) {
	if rotation%2 != 0 {
		return d.Height, d.Width
	}
	return d.Width, d.Height
}

// Point 使用当前显示方向的归一化坐标，边界为 [0,1]。
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func (p Point) Valid() bool {
	return !math.IsNaN(p.X) && !math.IsNaN(p.Y) && !math.IsInf(p.X, 0) && !math.IsInf(p.Y, 0) && p.X >= 0 && p.X <= 1 && p.Y >= 0 && p.Y <= 1
}
func (p Point) Natural(rotation int) Point {
	switch rotation {
	case 1:
		return Point{p.Y, 1 - p.X}
	case 2:
		return Point{1 - p.X, 1 - p.Y}
	case 3:
		return Point{1 - p.Y, p.X}
	default:
		return p
	}
}

// Contact 的 ID 在一次触摸生命周期内固定，最多支持十指。
type Contact struct {
	ID int `json:"id"`
	Point
}

// Frame 是某时刻全部按下的手指；空列表表示全部抬起，AtMS 从手势起点计时。
type Frame struct {
	AtMS     int       `json:"atMs"`
	Contacts []Contact `json:"contacts"`
}

type DeviceInfo struct {
	OS        string `json:"os"`
	OSVersion string `json:"osVersion"`
	Model     string `json:"model"`
	Name      string `json:"name"`
	// Identifier 是宿主可获得的 vendor 标识，不承诺是硬件序列号或跨重装不变。
	Identifier string  `json:"identifier"`
	Battery    float64 `json:"battery"`
	Display    Display `json:"display"`
}

// Capabilities 只表示当前进程可解析到接口，不表示系统已经授权或真机验收通过。
type Capabilities struct {
	Touch         bool `json:"touch"`
	Keyboard      bool `json:"keyboard"`
	Unicode       bool `json:"unicode"`
	Capture       bool `json:"capture"`
	Video         bool `json:"video"`
	LaunchApp     bool `json:"launchApp"`
	ForegroundApp bool `json:"foregroundApp"`
	LockScreen    bool `json:"lockScreen"`
}
