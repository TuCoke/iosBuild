// Package app 负责 iOS Agent 的独立装配与生命周期，不导入安卓 app/commands/core。
package app

import (
	"autogov1/v2/agent/ios/runtime"
	"autogov1/v2/agent/transport"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Status struct {
	Running   bool   `json:"running"`
	State     string `json:"state"`
	Server    string `json:"server,omitempty"`
	LastError string `json:"lastError,omitempty"`
}
type Manager struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	state  Status
	fault  error
}

func (m *Manager) Status() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.state }
func (m *Manager) Start(parent context.Context, d runtime.Device, c runtime.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if parent == nil {
		return fmt.Errorf("Agent context 不能为空")
	}
	if m.fault != nil {
		return fmt.Errorf("上次资源清理未确认，请核对现场并重启宿主后再连接: %w", m.fault)
	}
	if m.cancel != nil {
		return fmt.Errorf("iOS Agent 已在运行，请先停止再更换配置")
	}
	c.OnState = func(s string) {
		m.mu.Lock()
		m.state.State = s
		if s == "online" {
			m.state.LastError = ""
		}
		m.mu.Unlock()
	}
	session, e := runtime.NewSession(d, c)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.done = make(chan struct{})
	done := m.done
	m.state = Status{Running: true, State: "connecting", Server: c.Server}
	go func() {
		defer close(done)
		defer func() {
			m.mu.Lock()
			m.cancel = nil
			m.state.Running = false
			if m.fault != nil {
				m.state.State = "cleanup_failed"
				m.state.LastError = "资源清理未确认，需要检查现场"
			} else {
				m.state.State = "stopped"
			}
			m.mu.Unlock()
		}()
		for ctx.Err() == nil {
			err := session.Run(ctx)
			if fault := session.CleanupError(); fault != nil {
				m.mu.Lock()
				m.fault = fault
				m.mu.Unlock()
				return
			}
			if ctx.Err() != nil {
				return
			}
			delay := 3 * time.Second
			if code, _, rejected := transport.RejectReason(err); rejected && code >= 4000 {
				delay = time.Minute
			}
			m.mu.Lock()
			m.state.State = "reconnecting"
			if err != nil {
				detail := err.Error()
				if c.Token != "" {
					detail = strings.ReplaceAll(detail, c.Token, "[凭据已隐藏]")
				}
				if len(detail) > 500 {
					detail = detail[:500]
				}
				m.state.LastError = detail
			}
			m.mu.Unlock()
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return nil
}
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	if cancel != nil {
		m.state.State = "stopping"
		cancel()
	}
	m.mu.Unlock()
	if cancel == nil {
		m.mu.Lock()
		err := m.fault
		m.mu.Unlock()
		return err
	}
	select {
	case <-done:
		m.mu.Lock()
		err := m.fault
		m.mu.Unlock()
		return err
	case <-ctx.Done():
		return fmt.Errorf("iOS Agent 仍在清理，尚未停止: %w", ctx.Err())
	}
}
