// Package apkctl 使用参考样本的本机 TCP 控制协议连接宿主 JavaScriptCore。
// 帧头为大端 messageID/length，帧体为大端 status 加长度前缀字符串；不再轮询事件。
package apkctl

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

type LifecycleEvent string

const (
	EventPause       LifecycleEvent = "pause"
	EventResume      LifecycleEvent = "resume"
	EventStop        LifecycleEvent = "stop"
	lifecycleContext                = "__autogo_lifecycle__"
	maxFrame                        = 16 << 20
)

type frame struct {
	id, status uint32
	body       []byte
	err        error
}
type event struct {
	frame
	contextID, message string
	connection         net.Conn
}

// Session 属于一次脚本运行，结束时关闭连接、清空回调并唤醒未完成请求。
// 原生调用不能由 Go 强制抢占；硬超时仍由工作进程的看护者负责。
type Session struct {
	ctx       context.Context
	cancel    context.CancelFunc
	address   string
	mu        sync.Mutex
	writeMu   sync.Mutex
	conn      net.Conn
	next      uint32
	pending   map[uint32]chan frame
	messageCB func(string, string)
	events    map[LifecycleEvent]func()
	incoming  chan event
}

func NewSession(ctx context.Context) *Session {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	address := os.Getenv("AGOV_APKCTL_ADDR")
	if address == "" {
		address = "127.0.0.1:8990"
	}
	s := &Session{ctx: runCtx, cancel: cancel, address: address, pending: map[uint32]chan frame{}, events: map[LifecycleEvent]func(){}, incoming: make(chan event, 64)}
	go s.dispatchEvents()
	context.AfterFunc(runCtx, func() { s.closeConnection(nil, runCtx.Err()) })
	return s
}

var defaultOnce sync.Once
var defaultSession *Session

func shared() *Session {
	defaultOnce.Do(func() { defaultSession = NewSession(context.Background()) })
	return defaultSession
}
func Eval(contextID, source string) string           { return shared().Eval(contextID, source) }
func SetCallback(callback func(string, string))      { shared().SetCallback(callback) }
func RegEvent(event LifecycleEvent, callback func()) { shared().RegEvent(event, callback) }
func (s *Session) Close()                            { s.cancel(); s.closeConnection(nil, context.Canceled) }
func (s *Session) SetCallback(callback func(string, string)) {
	s.mu.Lock()
	s.messageCB = callback
	s.mu.Unlock()
	if callback != nil {
		_, _ = s.connect()
	}
}
func (s *Session) RegEvent(event LifecycleEvent, callback func()) {
	s.mu.Lock()
	if callback == nil {
		delete(s.events, event)
	} else {
		s.events[event] = callback
	}
	s.mu.Unlock()
	if callback != nil {
		_, _ = s.connect()
	}
}
func (s *Session) Eval(contextID, source string) string {
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	result, err := s.eval(ctx, contextID, source)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[apkctl]", err)
		return ""
	}
	return result
}
func (s *Session) connect() (net.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if s.conn != nil {
		return s.conn, nil
	}
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(s.ctx, "tcp", s.address)
	if err != nil {
		return nil, err
	}
	s.conn = conn
	go s.read(conn)
	return conn, nil
}
func (s *Session) eval(ctx context.Context, contextID, source string) (string, error) {
	conn, err := s.connect()
	if err != nil {
		return "", err
	}
	var payload bytes.Buffer
	if err = writeString(&payload, contextID); err != nil {
		return "", err
	}
	if err = writeString(&payload, source); err != nil {
		return "", err
	}
	s.mu.Lock()
	if s.conn != conn {
		s.mu.Unlock()
		return "", io.ErrClosedPipe
	}
	s.next++
	if s.next == 0 {
		s.next++
	}
	id := s.next
	response := make(chan frame, 1)
	s.pending[id] = response
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	if err = s.write(conn, frame{id: id, status: 1, body: payload.Bytes()}); err != nil {
		return "", err
	}
	select {
	case result := <-response:
		if result.err != nil {
			return "", result.err
		}
		value, err := readString(bytes.NewReader(result.body))
		if err != nil {
			return "", err
		}
		if result.status != 0 {
			return "", fmt.Errorf("宿主执行失败(status=%d): %s", result.status, value)
		}
		return value, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func readFrame(reader io.Reader) (frame, error) {
	var header [8]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return frame{}, err
	}
	length := binary.BigEndian.Uint32(header[4:])
	if length < 4 || length > maxFrame {
		return frame{}, fmt.Errorf("控制帧长度无效: %d", length)
	}
	body := make([]byte, int(length))
	if _, err := io.ReadFull(reader, body); err != nil {
		return frame{}, err
	}
	return frame{id: binary.BigEndian.Uint32(header[:4]), status: binary.BigEndian.Uint32(body[:4]), body: body[4:]}, nil
}
func writeString(writer io.Writer, value string) error {
	if len(value) > maxFrame-4 {
		return fmt.Errorf("控制字符串过长")
	}
	if err := binary.Write(writer, binary.BigEndian, uint32(len(value))); err != nil {
		return err
	}
	_, err := io.WriteString(writer, value)
	return err
}
func readString(reader *bytes.Reader) (string, error) {
	var length uint32
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
		return "", err
	}
	if uint64(length) > uint64(reader.Len()) {
		return "", io.ErrUnexpectedEOF
	}
	data := make([]byte, int(length))
	_, err := io.ReadFull(reader, data)
	return string(data), err
}
func (s *Session) write(conn net.Conn, f frame) error {
	if len(f.body) > maxFrame-4 {
		return fmt.Errorf("控制帧过长")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var packet bytes.Buffer
	_ = binary.Write(&packet, binary.BigEndian, f.id)
	_ = binary.Write(&packet, binary.BigEndian, uint32(4+len(f.body)))
	_ = binary.Write(&packet, binary.BigEndian, f.status)
	packet.Write(f.body)
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := io.Copy(conn, &packet)
	if err != nil {
		s.closeConnection(conn, err)
	}
	return err
}
func (s *Session) read(conn net.Conn) {
	for {
		f, err := readFrame(conn)
		if err != nil {
			s.closeConnection(conn, err)
			return
		}
		if f.status == 5 || f.status == 6 {
			reader := bytes.NewReader(f.body)
			contextID, err := readString(reader)
			if err != nil {
				s.closeConnection(conn, err)
				return
			}
			message, err := readString(reader)
			if err != nil || reader.Len() != 0 {
				s.closeConnection(conn, io.ErrUnexpectedEOF)
				return
			}
			e := event{frame: f, contextID: contextID, message: message, connection: conn}
			select {
			case s.incoming <- e:
			case <-s.ctx.Done():
				return
			default:
				s.closeConnection(conn, errors.New("宿主事件积压超过限制"))
				return
			}
		} else {
			s.mu.Lock()
			pending := s.pending[f.id]
			if pending != nil {
				pending <- f
				delete(s.pending, f.id)
			}
			s.mu.Unlock()
		}
	}
}
func (s *Session) closeConnection(conn net.Conn, reason error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if conn != nil && s.conn != conn {
		return
	}
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
	for id, pending := range s.pending {
		pending <- frame{err: reason}
		delete(s.pending, id)
	}
	if s.ctx.Err() != nil {
		s.messageCB = nil
		s.events = map[LifecycleEvent]func(){}
	}
}
func (s *Session) dispatchEvents() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case e := <-s.incoming:
			if s.ctx.Err() != nil {
				return
			}
			s.mu.Lock()
			conn := s.conn
			if conn != e.connection {
				s.mu.Unlock()
				continue
			}
			messageCB := s.messageCB
			callback := s.events[LifecycleEvent(e.message)]
			s.mu.Unlock()
			status, message := uint32(0), ""
			func() {
				defer func() {
					if p := recover(); p != nil {
						status = 3
						message = fmt.Sprint(p)
					}
				}()
				if e.contextID == lifecycleContext {
					if callback != nil {
						callback()
					}
				} else if messageCB != nil {
					messageCB(e.contextID, e.message)
				}
			}()
			if e.status == 6 && conn != nil {
				var body bytes.Buffer
				if status != 0 {
					_ = writeString(&body, message)
				}
				_ = s.write(conn, frame{id: e.id, status: status, body: body.Bytes()})
			}
		}
	}
}
