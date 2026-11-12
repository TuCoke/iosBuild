// Package gesture 是拟人化手势的【参数层】:按压时长、落指前停顿、时长抖动、
// 落点偏移。只做纯计算,不碰设备、不注入事件。
//
// 【为什么单独拎出来】这些参数原来散在 motion.go 里,而 motion.go 依赖 cgo 和
// /dev/input,只能在 linux/androoid 上编译 —— 于是"落点会不会跑出控件""时长抖动
// 是不是真的只有 ±8%"这类约束没法在开发机上测,只能上真机撞。
// 抽成纯计算包后,约束可以直接用单测钉住,两边(agent 与 SDK)也共用同一份参数,
// 不会出现"改了 agent、SDK 还是老样子"。
package gesture

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	mu  sync.Mutex // math/rand 的全局源并发不安全,注入可能并发进来
	rng = rand.New(rand.NewSource(time.Now().UnixNano()))
)

// Float 返回 [0,1) 随机数。
func Float() float64 {
	mu.Lock()
	defer mu.Unlock()
	return rng.Float64()
}

// Between 返回 [lo, hi) 内的随机整数。
func Between(lo, hi int) int {
	if hi <= lo {
		return lo
	}
	return lo + int(Float()*float64(hi-lo))
}

// Gauss 标准正态随机数,截断到 ±2σ。
//
// 用正态而不是均匀:FFitts law(手指版 Fitts 定律)指出手指点击存在一个与目标
// 大小无关的绝对误差项,近似正态;截断是为了掐掉长尾,免得偶尔的大偏移把落点带出控件。
func Gauss() float64 {
	mu.Lock()
	defer mu.Unlock()
	for i := 0; i < 8; i++ {
		if v := rng.NormFloat64(); math.Abs(v) <= 2 {
			return v
		}
	}
	return 0
}

// Hold 一次点击的按压时长:50~120ms。
//
// 低于 50ms 时部分 ROM 会把这次触摸判成系统手势/误触(尤其带边缘返回手势的机型);
// 高于 120ms 又逼近各家 300~500ms 的长按判定下限,余量不够。
func Hold() int { return Between(50, 120) }

// PreDelay 落指前的准备停顿:30~120ms。
//
// 机器注入是"算完坐标立刻按下",事件间隔严丝合缝;真人抬手→看屏幕→再落指之间
// 必然有一段无事件的空白,这段空白在事件流里是最稳定的自动化特征之一。
func PreDelay() time.Duration {
	return time.Duration(Between(30, 120)) * time.Millisecond
}

// JitterDur 手势时长抖动 ±8%:同一个手势两次不该分毫不差。
func JitterDur(durMs int) int {
	if durMs <= 0 {
		return durMs
	}
	return durMs + Between(-durMs/12, durMs/12+1)
}

// InnerOffset 在控件的一条边上取落点偏移(像素)。
//
//   - 偏移不超过边长的 1/5,且最多 12px;
//   - 短于 24px 的边不偏 —— 这个尺度上任何偏移都可能出界或被判成误触;
//   - 偏移按正态取、截断在 ±limit,保证落点始终在控件内部。
func InnerOffset(size int) int {
	if size < 24 {
		return 0
	}
	limit := size / 5
	if limit > 12 {
		limit = 12
	}
	if limit < 1 {
		return 0
	}
	off := int(math.Round(Gauss() * float64(limit) / 2)) // ±2σ = limit
	if off > limit {
		return limit
	}
	if off < -limit {
		return -limit
	}
	return off
}

// FrameMs 一次触摸轨迹里"每帧"的间隔(毫秒)。
//
// 【为什么必须可配】触摸上报率是面板属性,不是常量:实测这台 Xiaomi 13 是 8.2ms
// (≈120Hz),而老机型可能只有 16ms(≈60Hz)。写死任何一个值都会在一部分设备上露馅 ——
// 帧率只有面板一半,事件密度就不对。
//
// 默认 8ms(覆盖绝大多数现代触控面板),可用环境变量 AUTOGO_FRAME_MS 覆盖,
// 夹在 6~20ms:低于 6 没有面板支持,高于 20 就接近"拖影"了。
func FrameMs() int {
	ms := 8
	if raw := os.Getenv("AUTOGO_FRAME_MS"); raw != "" {
		if v, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && v > 0 {
			ms = v
		}
	}
	if ms < 6 {
		ms = 6
	}
	if ms > 20 {
		ms = 20
	}
	return ms
}

// Wander 生成沿"垂线方向"的横向摆动(归一化屏幕单位),长度 = steps。
//
// 【为什么需要它】实测真人滑动:路径偏离两端连线 0.03~0.22 屏,横向漂移最大 0.37 屏;
// 而原来只加了一条 8~12Hz、0.35dp 的微震颤(≈0.001 屏)+ 3~8% 的控制点偏移 ——
// 抖动幅度只有真手的 1/10~1/20,轨迹看上去"太顺"。
//
// 这里补的是【低频摆动】(0.7~1.5Hz,一次滑动里摆多半到一整个来回),
// 幅度按滑动距离取 9%(夹在 0.006~0.02 屏之间),两端渐隐:
// 起手和抬手时最贴近直线,中段最松 —— 与真人数据一致,也保证终点不偏。
func Wander(steps int, distNorm float64) []float64 {
	if steps <= 0 {
		return nil
	}
	amp := distNorm * 0.09
	if amp < 0.006 {
		amp = 0.006
	}
	if amp > 0.02 {
		amp = 0.02
	}
	amp *= 0.8 + 0.4*Float() // 每次振幅不同
	if amp > 0.02 {          // 抖动后仍要守住上界,否则峰值会冲到 0.024
		amp = 0.02
	}
	cycles := 0.7 + 0.8*Float()
	phase := Float() * 2 * math.Pi
	out := make([]float64, steps)
	for i := 0; i < steps; i++ {
		p := float64(i+1) / float64(steps)
		// 两端渐隐:前 15% 与后 15% 线性收敛到 0
		taper := 1.0
		if p < 0.15 {
			taper = p / 0.15
		} else if p > 0.85 {
			taper = (1 - p) / 0.15
		}
		out[i] = amp * math.Sin(2*math.Pi*(cycles*p+phase)) * taper
	}
	return out
}

// MaxContacts与原生输入驱动保持一致；实际可用数量还受设备能力限制。
const MaxContacts = 10

// Contact是一帧中的活动触点，ID在同一手势内保持稳定。可选轴均为0..1归一化值。
type Contact struct {
	ID          int      `json:"id"`
	X           float64  `json:"x"`
	Y           float64  `json:"y"`
	Pressure    *float64 `json:"pressure,omitempty"`
	TouchMajor  *float64 `json:"touchMajor,omitempty"`
	TouchMinor  *float64 `json:"touchMinor,omitempty"`
	WidthMajor  *float64 `json:"widthMajor,omitempty"`
	WidthMinor  *float64 `json:"widthMinor,omitempty"`
	Orientation *float64 `json:"orientation,omitempty"`
}

// Frame是相对起点atMs时刻的全部活动触点；空contacts表示全部抬起。
type Frame struct {
	AtMS     int       `json:"atMs"`
	Contacts []Contact `json:"contacts"`
}

// ValidateFrames在云端、设备端和本地SDK共用；有界轨迹不能夹带重复触点或非有限坐标。
func ValidateFrames(frames []Frame) error {
	if len(frames) < 1 || len(frames) > 2048 || frames[0].AtMS != 0 || len(frames[0].Contacts) == 0 {
		return fmt.Errorf("轨迹需1..2048帧，首帧从0毫秒开始且至少有一个触点")
	}
	previous := 0
	for _, frame := range frames {
		if frame.AtMS < previous || frame.AtMS > 120000 {
			return fmt.Errorf("轨迹时间超过限制或发生倒退")
		}
		previous = frame.AtMS
		if err := ValidateContacts(frame.Contacts); err != nil {
			return err
		}
	}
	return nil
}

// ValidateContacts同时用于实时快照和完整轨迹，防止两种入口的限制不一致。
func ValidateContacts(points []Contact) error {
	if len(points) > MaxContacts {
		return fmt.Errorf("同帧最多十个触点")
	}
	seen := 0
	for _, p := range points {
		if p.ID < 0 || p.ID >= MaxContacts || seen&(1<<p.ID) != 0 {
			return fmt.Errorf("触点ID无效或在同帧重复")
		}
		seen |= 1 << p.ID
		for _, v := range []*float64{&p.X, &p.Y, p.Pressure, p.TouchMajor, p.TouchMinor, p.WidthMajor, p.WidthMinor, p.Orientation} {
			if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 1) {
				return fmt.Errorf("触点坐标及可选轴必须是0..1有限数值")
			}
		}
	}
	return nil
}

// ToNatural是Android显示旋转的逆变换，把当前屏幕坐标映射回触摸面板的自然朝向。
func ToNatural(x, y float64, rotation int) (float64, float64) {
	switch rotation & 3 {
	case 1:
		return 1 - y, x
	case 2:
		return 1 - x, 1 - y
	case 3:
		return y, 1 - x
	default:
		return x, y
	}
}
