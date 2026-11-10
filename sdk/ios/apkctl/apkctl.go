// Package apkctl 对齐 AutoGo iOS 的外壳控制 API（3 个函数 + 生命周期事件）。
//
// 这层是"外壳 ↔ 脚本"的通道：
//   · Eval     外壳请求脚本执行一段 JS（AutoGo 里是 rhino 引擎，iOS 侧同样是 JS）
//   · SetCallback 脚本侧注册一个"收到外壳消息"的回调
//   · RegEvent 注册暂停/恢复/停止这类生命周期回调
//
// 我们与 AutoGo 的差别只在「事件怎么到达 Go」：它由原生直接回调，
// 我们没有原生回调表，用一个轻量轮询把事件拉回来（300ms 一次，开销可忽略）。
package apkctl

import (
	"sync"
	"time"

	"autogov1/v2/sdk/ios/internal/bridge"
)

// LifecycleEvent 表示脚本运行过程中的控制事件。
type LifecycleEvent string

const (
	// EventPause 表示脚本暂停事件。
	EventPause LifecycleEvent = "pause"
	// EventResume 表示脚本恢复事件。
	EventResume LifecycleEvent = "resume"
	// EventStop 表示脚本停止事件。
	EventStop LifecycleEvent = "stop"
)

var (
	mu            sync.Mutex
	messageCB     func(contextID, message string)
	eventCB       = map[LifecycleEvent]func(){}
	polling       bool
	pollStop      chan struct{}
)

// Eval 让外壳执行一段脚本，返回执行结果（同步）。
func Eval(contextID, script string) string {
	var out struct {
		Result string `json:"result"`
	}
	if !bridge.DoTimeout("apkctl.eval", map[string]any{"contextId": contextID, "script": script}, &out, 30*time.Second) {
		return ""
	}
	return out.Result
}

// SetCallback 注册"外壳发来消息"的回调；传 nil 取消。
func SetCallback(callback func(contextID, message string)) {
	mu.Lock()
	messageCB = callback
	shouldStart := callback != nil && !polling
	mu.Unlock()
	if shouldStart {
		startPolling()
	}
}

// RegEvent 注册生命周期事件回调。
func RegEvent(event LifecycleEvent, callback func()) {
	mu.Lock()
	if callback == nil {
		delete(eventCB, event)
	} else {
		eventCB[event] = callback
	}
	needStart := len(eventCB) > 0 && !polling
	mu.Unlock()
	if needStart {
		startPolling()
	}
	_ = bridge.Do("apkctl.reg_event", map[string]any{"event": string(event)}, nil)
}

// startPolling 起一个后台循环把原生事件拉回来。
// 只起一次；没有订阅者时自动退出，避免常驻。
func startPolling() {
	mu.Lock()
	if polling {
		mu.Unlock()
		return
	}
	polling = true
	pollStop = make(chan struct{})
	stop := pollStop
	mu.Unlock()

	go func() {
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				mu.Lock()
				empty := messageCB == nil && len(eventCB) == 0
				mu.Unlock()
				if empty {
					mu.Lock()
					polling = false
					mu.Unlock()
					return
				}
				var out struct {
					Events  []string `json:"events"`
					Context string   `json:"contextId"`
					Message string   `json:"message"`
				}
				if !bridge.Do("apkctl.poll", nil, &out) {
					continue
				}
				if out.Message != "" {
					mu.Lock()
					cb := messageCB
					mu.Unlock()
					if cb != nil {
						cb(out.Context, out.Message)
					}
				}
				for _, name := range out.Events {
					mu.Lock()
					cb := eventCB[LifecycleEvent(name)]
					mu.Unlock()
					if cb != nil {
						cb()
					}
				}
			}
		}
	}()
}
