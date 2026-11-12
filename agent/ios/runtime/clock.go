package runtime

import "time"

// serverClock 把服务端期限转换为本机单调计时；手机调时或时区变化不改变任务预算。
// 起点取注册发送前，故把握手往返耗时全部扣除，宁可提前拒绝，不延长服务端期限。
type serverClock struct {
	serverMS int64
	anchor   time.Time
}

func (c serverClock) budget(deadlineMS int64) (time.Duration, bool) {
	if deadlineMS <= 0 {
		return 0, true
	}
	if c.serverMS <= 0 || c.anchor.IsZero() {
		return 0, false
	}
	// 先限制差值再转 Duration，防止不可信时间值溢出。
	remaining := deadlineMS - c.serverMS
	if deadlineMS < c.serverMS || remaining < 0 {
		return 0, false
	}
	remaining -= time.Since(c.anchor).Milliseconds()
	if remaining <= 0 {
		return 0, false
	}
	if remaining > 125000 {
		remaining = 125000
	}
	return time.Duration(remaining) * time.Millisecond, true
}
