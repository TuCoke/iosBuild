package runtime

import (
	"autogov1/v2/agent/kernel"
	"autogov1/v2/agent/transport"
	"autogov1/v2/protocol"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Config 的数据目录必须在手机本机；凭据不写入命令正文、日志或注册回执。
type Config struct {
	Server, DataDir, Token string
	AllowInsecure          bool
	OnState                func(string)
}
type Session struct {
	phone       Device
	config      Config
	journal     *kernel.Journal
	mu          sync.Mutex
	active      map[string]context.CancelFunc
	cleanupErr  error
	videoMu     sync.Mutex
	videoCancel context.CancelFunc
	videoDone   chan struct{}
	videoSource VideoSource
	videoError  error
}

func NewSession(phone Device, cfg Config) (*Session, error) {
	if phone == nil {
		return nil, fmt.Errorf("缺少 iOS 设备驱动")
	}
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	j, err := kernel.Open(filepath.Join(cfg.DataDir, "journal"))
	if err != nil {
		return nil, err
	}
	return &Session{phone: phone, config: cfg, journal: j, active: map[string]context.CancelFunc{}}, nil
}

func (s *Session) Run(ctx context.Context) error {
	if e := s.CleanupError(); e != nil {
		return e
	}
	unlock, lockErr := lockInstance(s.config.DataDir)
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	info, e := s.phone.Info(ctx)
	if e != nil {
		return e
	}
	if e = s.phone.Healthy(ctx); e != nil {
		return e
	}
	if info.OS != "ios" || info.Identifier == "" || info.Display.Width <= 0 || info.Display.Height <= 0 {
		return fmt.Errorf("iOS 设备身份或屏幕信息不可用")
	}
	id, e := deviceIdentity(s.config.DataDir, info.Identifier)
	if e != nil {
		return e
	}
	sid := "is" + rand.Text()
	conn, e := transport.DialAuthenticated(ctx, s.config.Server, s.config.Token)
	if e != nil {
		return e
	}
	defer conn.Close()
	stopDial := context.AfterFunc(ctx, conn.Close)
	defer stopDial()
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	reg := protocol.AgentMsg{Type: "register", DeviceID: id, Name: info.Name + "（iOS Agent）", Model: info.Model, Width: info.Display.Width, Height: info.Display.Height, Platform: "ios", OSVersion: info.OSVersion, SessionID: sid, Features: []string{protocol.ActionAcknowledgmentV1, protocol.CommandJournalV1, protocol.CommandFenceV1, protocol.RegistrationAckV1, "ios_native_agent_v1"}, Commands: protocol.NormalizeCommands(append(s.phone.Commands(), "command_cancel", "command_result", "screenshot", "snap", "video_start", "video_stop", "video_key", "video_cfg"))}
	if e = conn.WriteJSON(reg); e != nil {
		return e
	}
	b, e := conn.ReadRegistration(10 * time.Second)
	if e != nil {
		return e
	}
	var ack protocol.RegistrationAck
	if json.Unmarshal(b, &ack) != nil || ack.Type != "register_ack" || ack.DeviceID != id || ack.SessionID != sid {
		return fmt.Errorf("本地服务端注册确认不匹配")
	}
	stopPing := conn.StartHeartbeat()
	defer stopPing()
	log.Printf("iOS Agent 已上线: %s，%s，%dx%d", id, info.OSVersion, info.Display.Width, info.Display.Height)
	if s.config.OnState != nil {
		s.config.OnState("online")
		defer s.config.OnState("disconnected")
	}
	send := func(r protocol.CommandReceipt) {
		msg := map[string]any{"type": "op_result", "deviceId": id, "id": r.ID, "state": r.State, "ok": r.OK, "msg": r.Msg}
		// 无数据回执必须省略 data；JSON null 会被 MCP 当成正文，吞掉前台应用名。
		if r.Data != nil {
			msg["data"] = r.Data
		}
		_ = conn.WriteJSON(msg)
	}
	reply := func(c protocol.Cmd, state string, ok bool, msg string, data any) {
		send(protocol.CommandReceipt{ID: c.ID, State: state, OK: ok, Msg: msg, Data: data})
	}
	actions := make(chan protocol.Cmd, 64)
	observations := make(chan protocol.Cmd, 8)
	media := make(chan protocol.Cmd, 8)
	var wg sync.WaitGroup
	// 所有写动作单路有界执行；原生调用取消后必须真实返回，才允许下一动作。
	wg.Add(1)
	go func() {
		defer wg.Done()
		pressed := false
		defer func() {
			if pressed {
				clean, done := context.WithTimeout(context.Background(), 5*time.Second)
				defer done()
				if _, err := s.phone.Execute(clean, protocol.Cmd{Cmd: "input_reset"}); err != nil {
					s.failCleanup(err)
				}
			}
		}()
		for c := range actions {
			if c.Cmd == "touch" && c.ID == "" {
				if run.Err() != nil {
					continue
				}
				if c.Action != 1 {
					pressed = true
				}
				_, err := s.phone.Execute(run, c)
				if err != nil {
					log.Printf("iOS 实时触摸失败: %v", err)
					cancel()
					conn.Close()
					continue
				}
				pressed = c.Action != 1
				continue
			}
			s.executeRecorded(run, c, send)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for c := range observations {
			if run.Err() != nil {
				return
			}
			task, done := context.WithTimeout(run, 15*time.Second)
			switch c.Cmd {
			case "ui_dump":
				nodes, err := s.phone.Tree(task)
				_ = conn.WriteJSON(map[string]any{"type": "ui_result", "deviceId": id, "id": c.ID, "ok": err == nil, "msg": message(err), "nodes": nodes})
			case "screenshot", "snap":
				jpg, err := s.phone.Screenshot(task)
				if err == nil {
					typ := transport.BinScreenshot
					if c.Cmd == "snap" {
						typ = transport.BinThumbnail
					}
					err = conn.WriteBinary(typ, jpg)
				}
				if err != nil {
					reply(c, "returned", false, message(err), nil)
				}
			default:
				data, err := s.phone.Execute(task, c)
				if c.Cmd == "current_app" && err == nil {
					var foreground string
					raw, _ := json.Marshal(data)
					if json.Unmarshal(raw, &foreground) == nil {
						reply(c, "returned", true, foreground, nil)
					} else {
						reply(c, "returned", false, "iOS 前台应用回执格式无效", nil)
					}
				} else {
					reply(c, "returned", err == nil, message(err), data)
				}
			}
			done()
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for c := range media {
			if run.Err() != nil {
				return
			}
			var err error
			switch c.Cmd {
			case "video_start":
				err = s.startVideo(run, conn, c)
			case "video_stop":
				err = s.stopVideo()
			case "video_key":
				err = s.videoCommand("keyframe", 0)
			case "video_cfg":
				err = s.videoCommand("bitrate", c.Bitrate)
			}
			if c.ID != "" {
				reply(c, "returned", err == nil, message(err), nil)
			}
			if err != nil {
				log.Printf("iOS 投屏: %v", err)
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(3 * time.Second)
		defer tick.Stop()
		failures := 0
		for {
			select {
			case <-run.Done():
				return
			case <-tick.C:
				check, done := context.WithTimeout(run, 8*time.Second)
				err := s.phone.Healthy(check)
				done()
				if err != nil {
					failures++
					if failures >= 3 {
						log.Printf("iOS 连续健康探测失败，撤销本地上线")
						cancel()
						conn.Close()
						return
					}
					continue
				}
				failures = 0
				infoCtx, infoDone := context.WithTimeout(run, 2*time.Second)
				if fresh, err := s.phone.Info(infoCtx); err == nil {
					info = fresh
				}
				infoDone()
				_ = conn.WriteJSON(map[string]any{"type": "heartbeat", "battery": batteryPercent(info.Battery)})
				s.videoMu.Lock()
				videoActive := s.videoCancel != nil
				s.videoMu.Unlock()
				if !videoActive {
					select {
					case observations <- protocol.Cmd{Cmd: "snap"}:
					default:
					}
				}
			}
		}
	}()
	defer func() {
		cancel()
		conn.Close()
		close(actions)
		close(observations)
		close(media)
		wg.Wait()
		if err := s.stopVideo(); err != nil {
			s.failCleanup(err)
		}
	}()
	go func() { <-run.Done(); conn.Close() }()
	for {
		b, e := conn.ReadText()
		if e != nil {
			return e
		}
		var c protocol.Cmd
		if json.Unmarshal(b, &c) != nil {
			continue
		}
		// 现有投屏入口会发无请求标识的 wake 提示；它没有写入身份，不能借此绕过栅栏。
		// 屏幕已亮时无需动作；需要亮屏时提示用户，正式 wake 仍走受控执行链路。
		if c.Cmd == "wake" && c.ID == "" {
			check, done := context.WithTimeout(run, 3*time.Second)
			var state struct {
				On bool `json:"on"`
			}
			data, err := s.phone.Execute(check, protocol.Cmd{Cmd: "screen_state"})
			if err == nil {
				b, _ := json.Marshal(data)
				err = json.Unmarshal(b, &state)
			}
			done()
			if err == nil && !state.On {
				reply(c, "rejected", false, "iPhone 已息屏，请解锁；自动唤醒需使用受控 wake 命令", nil)
			}
			continue
		}
		if c.Cmd == "server_endpoints" {
			continue
		}
		if err := protocol.ValidateCommand(c); err != nil {
			reply(c, "rejected", false, err.Error(), nil)
			continue
		}
		if c.SessionID != "" && c.SessionID != sid {
			reply(c, "rejected", false, "连接代次失效", nil)
			continue
		}
		if c.Cmd == "command_result" || c.Cmd == "command_cancel" {
			if c.SessionID != sid {
				reply(c, "rejected", false, "缺少连接代次", nil)
				continue
			}
			var r protocol.CommandReceipt
			var err error
			if c.Cmd == "command_result" {
				r, err = s.journal.Result(c.RequestID)
			} else {
				s.mu.Lock()
				f := s.active[c.RequestID]
				if f != nil {
					f()
				}
				s.mu.Unlock()
				r, err = s.journal.Cancel(c.RequestID)
			}
			if err != nil {
				reply(c, "rejected", false, "原命令记录不可读", nil)
			} else {
				reply(c, "returned", true, "", r)
			}
			continue
		}
		var lane chan protocol.Cmd
		switch c.Cmd {
		case "video_start", "video_stop", "video_key", "video_cfg":
			lane = media
		case "screenshot", "snap":
			lane = observations
		default:
			if !s.supports(c.Cmd) {
				log.Printf("iOS 本地适配未声明命令: %s", c.Cmd)
				reply(c, "rejected", false, fmt.Sprintf("iOS 本地适配未声明命令: %s", c.Cmd), nil)
				continue
			}
			if readOnly(c) {
				lane = observations
			} else {
				lane = actions
			}
		}
		if lane == actions && !(c.Cmd == "touch" && c.ID == "") {
			if c.ID == "" || c.SessionID != sid || c.Fence <= 0 {
				reply(c, "rejected", false, "写动作缺少请求身份或持久栅栏", nil)
				continue
			}
			receipt, fresh, err := s.journal.Reserve(c)
			if err != nil {
				reply(c, "rejected", false, "无法预约执行日志", nil)
				continue
			}
			if !fresh {
				send(receipt)
				continue
			}
		}
		if c.Cmd == "touch" && c.ID == "" && c.SessionID != sid {
			reply(c, "rejected", false, "触摸连接代次失效", nil)
			continue
		}
		select {
		case lane <- c:
		default:
			if lane == actions {
				if c.ID != "" {
					r, err := s.journal.Cancel(c.ID)
					if err == nil {
						send(r)
					}
				} else {
					cancel()
					conn.Close()
					return fmt.Errorf("触摸队列满，断开并清理按下状态")
				}
			} else {
				reply(c, "rejected", false, "iOS 命令队列已满", nil)
			}
		}
	}
}
func (s *Session) executeRecorded(ctx context.Context, c protocol.Cmd, send func(protocol.CommandReceipt)) {
	if ctx.Err() != nil || (c.DeadlineAt > 0 && time.Now().UnixMilli() >= c.DeadlineAt) {
		r, e := s.journal.Cancel(c.ID)
		if e == nil {
			send(r)
		}
		return
	}
	if e := s.journal.AcceptFence(c.Fence, c.ID); e != nil {
		r, _ := s.journal.Cancel(c.ID)
		send(r)
		return
	}
	task, cancel := context.WithTimeout(ctx, 125*time.Second)
	if c.DeadlineAt > 0 {
		var stop context.CancelFunc
		task, stop = context.WithDeadline(task, time.UnixMilli(c.DeadlineAt))
		defer stop()
	}
	defer cancel()
	s.mu.Lock()
	s.active[c.ID] = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.active, c.ID); s.mu.Unlock() }()
	started, e := s.journal.Start(c.ID)
	if e != nil || !started {
		r, e := s.journal.Result(c.ID)
		if e == nil {
			send(r)
		}
		return
	}
	data, err := s.phone.Execute(task, c)
	state := "returned"
	if err != nil && (s.phone.Unknown(err) || task.Err() != nil) {
		state = "unknown"
	}
	r, e := s.journal.Complete(c.ID, state, err == nil, message(err), data)
	if e != nil {
		r = protocol.CommandReceipt{ID: c.ID, State: "unknown", Msg: "执行回执无法持久化，需要核对"}
	}
	send(r)
}

func (s *Session) supports(name string) bool {
	for _, n := range s.phone.Commands() {
		if n == name {
			return true
		}
	}
	return false
}
func readOnly(c protocol.Cmd) bool {
	return protocol.IsObservation(c) || c.Cmd == "ime_on" || c.Cmd == "ime_off"
}
func message(err error) string {
	if err == nil {
		return "iOS 调用已返回；业务结果仍需核对"
	}
	s := err.Error()
	if len(s) > 1200 {
		s = s[:1200]
	}
	return strings.ReplaceAll(s, "\n", " ")
}
func batteryPercent(v float64) int {
	if v < 0 {
		return -1
	}
	if v > 1 {
		v = 1
	}
	return int(v * 100)
}

// 清理失败不能通过重连清空；必须核对现场，不能把 goroutine 退出当成资源已释放。
func (s *Session) failCleanup(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupErr = fmt.Errorf("iOS 资源清理未确认: %w", err)
}
func (s *Session) CleanupError() error { s.mu.Lock(); defer s.mu.Unlock(); return s.cleanupErr }
