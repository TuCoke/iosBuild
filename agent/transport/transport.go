// Package transport 设备端与服务端的 WS 传输(线程安全写、二进制分帧)。
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"autogov1/v2/protocol"

	"github.com/gorilla/websocket"
)

// RejectReason 从 Run 返回的错误里取出服务端【主动拒绝接入】时带的原因。
//
// 服务端拒绝(名额满/已被移除/是别人的设备)时会发一个带原因文本的 WS 关闭帧(code 4000+)。
// 有了它,上层就能:①把人话打进日志(不再是一行冷冰冰的 close 1006)②对这种账号级的
// 永久拒绝退避重连,而不是每 3 秒空连一次刷屏。ok=false 表示是普通断线,照常快速重连。
func RejectReason(err error) (code int, reason string, ok bool) {
	var ce *websocket.CloseError
	if errors.As(err, &ce) && ce.Code >= 4000 {
		return ce.Code, ce.Text, true
	}
	return 0, "", false
}

// authToken 公网服务端鉴权 token;非空则拨号时附到每个 URL 上(?token= 或 &token=)。
// 覆盖所有端点(静态/发现/切公网),一处设置全生效。
var authToken string

// SetToken 设置鉴权 token(main 从 -token 传入;空=不带)。
func SetToken(t string) { authToken = t }

func withToken(url string) string {
	if authToken == "" || strings.Contains(url, "token=") {
		return url
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	return url + sep + "token=" + authToken
}

// Agent->Server 二进制帧类型
const (
	BinVideo      byte = protocol.BinVideo
	BinThumbnail  byte = protocol.BinThumbnail
	BinScreenshot byte = protocol.BinScreenshot
)

// Conn 包装 websocket 连接,写加锁(视频/心跳/结果可能并发写)。
type Conn struct {
	c   *websocket.Conn
	wmu sync.Mutex
}

// dialer 带超时的拨号器。
//
// 【为什么不能用 websocket.DefaultDialer】它的 HandshakeTimeout 是 0 = 不限,
// 于是连不通的端点要等【操作系统的 TCP 超时】——真机实测 32 秒。
// 而 agent 是按端点列表逐个试的:一条死地址(比如服务端下发过来的它自己的内网 IP)
// 排在前面,就意味着每轮重连先离线 32 秒。表现是"公网时好时坏",
// 而实际上大半时间都在等那条死地址超时。
//
// 8 秒的取法:公网 + TLS 握手在 4G 上偶尔要 3~5 秒,给一倍余量;
// 再长就失去"快速跳到下一个端点"的意义了。
var dialer = &websocket.Dialer{
	HandshakeTimeout: 8 * time.Second,
	NetDial: (&net.Dialer{
		Timeout:   6 * time.Second, // TCP 连接阶段
		KeepAlive: 30 * time.Second,
	}).Dial,
}

// WS 应用层探活参数(gorilla 官方 example 同款)。
//
// 每 pingPeriod 发一个 control ping;server(gorilla)收 ping 会自动回 pong,
// 收到 pong 就把读超时顺延 pongWait。pongWait 内既没命令也没 pong → ReadMessage 超时报错
// → 上层重连。两个作用:
//
//	① 灭屏 / 公网静止时仍有周期流量,挡运营商 NAT、frp 的空闲连接回收(通常 3~5 分钟);
//	② 半开连接(对端死了、TCP 没断)不再无限阻塞在 ReadMessage 上,pongWait 后就能发现并重连。
//
// pingPeriod 必须明显小于 pongWait,给一次 ping 的往返留余量。
const (
	pongWait   = 60 * time.Second
	pingPeriod = 54 * time.Second
)

func Dial(url string) (*Conn, error) { return dialContext(context.Background(), withToken(url), nil) }

// DialAuthenticated 用每次连接自己的凭据，iOS 与安卓进程互不覆盖鉴权状态。
func DialAuthenticated(ctx context.Context, url, token string) (*Conn, error) {
	h := http.Header{}
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	return dialContext(ctx, url, h)
}
func dialContext(ctx context.Context, url string, headers http.Header) (*Conn, error) {
	c, _, err := dialer.DialContext(ctx, url, headers)
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(8 << 20)
	// 读超时 + 收到 pong 顺延。ReadMessage 内部会处理 control 帧(pong 交给这个 handler)。
	_ = c.SetReadDeadline(time.Now().Add(pongWait))
	c.SetPongHandler(func(string) error {
		return c.SetReadDeadline(time.Now().Add(pongWait))
	})
	return &Conn{c: c}, nil
}

// StartHeartbeat 起一个 goroutine 每 pingPeriod 发 control ping;返回的 stop 由上层读循环
// 退出时调用,goroutine 随之结束。ping 的写走 wmu(与业务写互斥)。写不出去说明连接已死,
// 直接结束 goroutine —— 读那边会因读超时 / TCP 断报错,触发重连。
func (t *Conn) StartHeartbeat() (stop func()) {
	done := make(chan struct{})
	go func() {
		tk := time.NewTicker(pingPeriod)
		defer tk.Stop()
		for {
			select {
			case <-done:
				return
			case <-tk.C:
				t.wmu.Lock()
				err := t.c.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				t.wmu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

func (t *Conn) WriteJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	return t.writeBounded(websocket.TextMessage, data)
}

// WriteBinary 发送 [type][payload]。
func (t *Conn) WriteBinary(typ byte, payload []byte) error {
	buf := make([]byte, 1+len(payload))
	buf[0] = typ
	copy(buf[1:], payload)
	t.wmu.Lock()
	defer t.wmu.Unlock()
	return t.writeBounded(websocket.BinaryMessage, buf)
}

// 调用方持 wmu；慢接收端不能无限占写锁，写超时后整条连接必须重建。
func (t *Conn) writeBounded(kind int, data []byte) error {
	_ = t.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err := t.c.WriteMessage(kind, data)
	if err != nil {
		t.Close()
		return err
	}
	_ = t.c.SetWriteDeadline(time.Time{})
	return nil
}

// ReadText 读一条文本消息(命令);非文本帧返回 nil,nil 由调用方跳过。
func (t *Conn) ReadText() ([]byte, error) {
	mt, data, err := t.c.ReadMessage()
	if err != nil {
		return nil, err
	}
	if mt != websocket.TextMessage {
		return nil, nil
	}
	return data, nil
}

func (t *Conn) Close() { _ = t.c.Close() }

// ReadRegistration 限时读取注册确认；超时后的连接必须关闭，不能继续沿用。
func (t *Conn) ReadRegistration(timeout time.Duration) ([]byte, error) {
	if err := t.c.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	data, err := t.ReadText()
	if err == nil {
		err = t.c.SetReadDeadline(time.Now().Add(pongWait))
	}
	return data, err
}
