// Package api 将参数、输出和能力调用绑定到一次运行，解释器之间不共享任务状态。
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"autogov1/v2/sdk/ios/internal/native"
)

type Args = map[string]any

// Session 由 Runner 独立创建，脚本的 goroutine 只能访问创建它的那次任务。
type Session struct {
	mu     sync.Mutex
	ctx    context.Context
	params map[string]any
	logs   []string
	result any
	output string
	bytes  int
	err    error
}

func NewSession(ctx context.Context, params map[string]any) (*Session, error) {
	if ctx == nil {
		return nil, fmt.Errorf("脚本 context 不能为空")
	}
	// 深拷贝参数，不能把宿主的可变 map 直接暴露给脚本。
	b, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("脚本参数必须可以编码为 JSON: %w", err)
	}
	if len(b) > 4<<20 {
		return nil, fmt.Errorf("脚本参数超过 4 MiB")
	}
	var copy map[string]any
	if err = json.Unmarshal(b, &copy); err != nil {
		return nil, err
	}
	return &Session{ctx: ctx, params: copy}, nil
}
func (s *Session) Error() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *Session) Logs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]string(nil), s.logs...)
	if s.output != "" {
		out = append(out, s.output)
	}
	return out
}
func (s *Session) Result() any { s.mu.Lock(); defer s.mu.Unlock(); return s.result }
func (s *Session) appendLocked(line string) {
	if s.bytes+len(s.output)+len(line) > 1<<20 || len(s.logs) >= 10000 {
		if s.err == nil {
			s.err = fmt.Errorf("脚本日志超过上限")
		}
		return
	}
	s.logs = append(s.logs, line)
	s.bytes += len(line)
}
func (s *Session) Log(args ...any) {
	if s.ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendLocked(strings.TrimSuffix(fmt.Sprintln(args...), "\n"))
}

// Write 承接解释器标准输出，保留未换行的尾部。
func (s *Session) Write(p []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(p)+s.bytes+len(s.output) > 1<<20 {
		s.err = fmt.Errorf("脚本输出超过上限")
		return 0, s.err
	}
	s.output += string(p)
	for {
		i := strings.IndexByte(s.output, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSuffix(s.output[:i], "\r")
		s.output = s.output[i+1:]
		s.appendLocked(line)
	}
	return len(p), nil
}
func (s *Session) Param(name string) any          { s.mu.Lock(); defer s.mu.Unlock(); return s.params[name] }
func (s *Session) ParamString(name string) string { v, _ := s.Param(name).(string); return v }
func (s *Session) ParamFloat(name string) float64 {
	switch v := s.Param(name).(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		n, _ := v.Float64()
		return n
	}
	return 0
}
func (s *Session) SetResult(value any) {
	if s.ctx.Err() != nil {
		return
	}
	b, err := json.Marshal(value)
	if err == nil && len(b) > 4<<20 {
		err = fmt.Errorf("脚本结果超过 4 MiB")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.err = fmt.Errorf("脚本结果无效: %w", err)
		return
	}
	var next any
	if err = json.Unmarshal(b, &next); err != nil {
		s.err = err
		return
	}
	s.result = next
}
func (s *Session) Sleep(ms int) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if ms <= 0 {
		return nil
	}
	t := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer t.Stop()
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case <-t.C:
		return nil
	}
}
func (s *Session) Call(op string, args Args) (map[string]any, error) {
	if op == "" {
		return nil, fmt.Errorf("缺少能力名")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	var out map[string]any
	if err := native.Call(ctx, op, args, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}
func (s *Session) CallString(op string, args Args, field string) (string, error) {
	out, err := s.Call(op, args)
	if err != nil {
		return "", err
	}
	value, _ := out[field].(string)
	return value, nil
}
func (s *Session) MustCall(op string, args Args) map[string]any {
	out, err := s.Call(op, args)
	if err != nil {
		panic(fmt.Sprintf("%s 失败: %v", op, err))
	}
	return out
}
