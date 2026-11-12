package debugapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"autogov1/v2/sdk/ios/video"
	"github.com/gorilla/websocket"
)

const videoHeaderSize = 40

// 仅允许同源页面和本机实验台；不能因为支持浏览器就开放全部网站的跨源控制。
func videoSocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" { // 非浏览器诊断客户端仍须通过首消息鉴权。
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return (u.Scheme == scheme && u.Host == r.Host) ||
		origin == "http://127.0.0.1:18790" || origin == "http://localhost:18790"
}

type videoSocketHello struct {
	Type    string         `json:"type"`
	Token   string         `json:"token"`
	Options map[string]any `json:"options"`
}

type videoSocketCommand struct {
	Type    string `json:"type"`
	Bitrate int    `json:"bitrate,omitempty"`
}

func socketJSON(conn *websocket.Conn, data any) error {
	_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	return conn.WriteJSON(data)
}

func (s *Server) videoSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		failure(w, 405, "视频 WebSocket 请使用 GET")
		return
	}
	upgrader := websocket.Upgrader{HandshakeTimeout: 5 * time.Second, CheckOrigin: videoSocketOrigin}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	v := s.cfg.Video
	v.mu.Lock()
	if v.shutdown {
		v.mu.Unlock()
		return
	}
	v.sockets[conn] = struct{}{}
	v.mu.Unlock()
	defer func() { v.mu.Lock(); delete(v.sockets, conn); v.mu.Unlock() }()
	conn.SetReadLimit(4096)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hello videoSocketHello
	if err = conn.ReadJSON(&hello); err != nil {
		return
	}
	// token 不放 URL、子协议或日志；鉴权成功之前不申请编码器和后台运行需求。
	if hello.Type != "start" || len(hello.Token) != len(s.token) ||
		subtle.ConstantTimeCompare([]byte(hello.Token), []byte(s.token)) != 1 {
		_ = socketJSON(conn, map[string]any{"type": "error", "error": "缺少或无效调试 token"})
		return
	}
	run, err := v.start(hello.Options, true)
	if err != nil {
		_ = socketJSON(conn, map[string]any{"type": "error", "error": err.Error()})
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_ = v.stop(ctx, run) // 只停止本连接创建的会话，不抢停其他调用者。
	}()
	if err = socketJSON(conn, map[string]any{"type": "started", "protocol": "AGV1", "headerBytes": videoHeaderSize}); err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(15 * time.Second)) })
	commands := make(chan videoSocketCommand, 8)
	readDone := make(chan struct{})
	readerExited := make(chan struct{})
	go func() {
		defer close(readerExited)
		defer close(readDone)
		for {
			var command videoSocketCommand
			if err := conn.ReadJSON(&command); err != nil {
				return
			}
			select {
			case commands <- command:
			default: // 不为失控客户端积累无界控制请求。
				return
			}
		}
	}()
	defer func() { _ = conn.Close(); <-readerExited }()
	ping := time.NewTicker(5 * time.Second)
	defer ping.Stop()
	var sequence uint64
	var configVersion uint32
	var signature [32]byte
	var width, height int
	writePacket := func(packet video.Packet) error {
		if configVersion == 0 && !packet.Keyframe {
			return nil
		}
		if packet.Keyframe {
			next := sha256.Sum256(videoParameters(packet.Data))
			if configVersion == 0 || next != signature || packet.Width != width || packet.Height != height {
				configVersion++
				signature, width, height = next, packet.Width, packet.Height
			}
		}
		sequence++
		message, err := videoSocketPacket(packet, sequence, configVersion)
		if err != nil {
			return err
		}
		_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		return conn.WriteMessage(websocket.BinaryMessage, message)
	}
	for {
		select {
		case packet := <-run.packets:
			if err = writePacket(packet); err != nil {
				return
			}
		case command := <-commands:
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			switch command.Type {
			case "stop":
				cancel()
				return
			case "keyframe":
				err = run.source.RequestKeyFrame(ctx)
			case "bitrate":
				if command.Bitrate < 64000 || command.Bitrate > 50000000 {
					err = fmt.Errorf("码率越界")
				} else {
					err = run.source.SetBitrate(ctx, command.Bitrate)
				}
			default:
				err = fmt.Errorf("未知视频控制类型")
			}
			cancel()
			reply := map[string]any{"type": "ack", "command": command.Type, "ok": err == nil}
			if err != nil {
				reply["error"] = err.Error()
			}
			if socketJSON(conn, reply) != nil {
				return
			}
		case <-ping.C:
			if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(3*time.Second)) != nil {
				return
			}
		case <-run.done:
			// 编码结束后先送完有界队列，避免与 done 同时就绪时丢掉最后几帧。
			for {
				select {
				case packet := <-run.packets:
					if writePacket(packet) != nil {
						return
					}
				default:
					v.mu.Lock()
					reason := run.err
					v.mu.Unlock()
					_ = socketJSON(conn, map[string]any{"type": "ended", "error": reason})
					return
				}
			}
		case <-readDone:
			return
		case <-r.Context().Done():
			return
		}
	}
}

// 一条 WS 二进制消息对应一个完整 Annex B 访问单元，不把 NAL 或网络分片当成视频帧。
// 40 字节大端头：magic[4]、flags[1]+保留[3]、sequence[8]、PTS微秒[8]、
// width[4]、height[4]、payloadLength[4]、configVersion[4]。
func videoSocketPacket(packet video.Packet, sequence uint64, configVersion uint32) ([]byte, error) {
	if len(packet.Data) == 0 || len(packet.Data) > 4<<20 || packet.PTS < 0 || packet.Width <= 0 || packet.Height <= 0 {
		return nil, fmt.Errorf("无效视频帧")
	}
	message := make([]byte, videoHeaderSize+len(packet.Data))
	copy(message, "AGV1")
	if packet.Keyframe {
		message[4] = 1
	}
	binary.BigEndian.PutUint64(message[8:16], sequence)
	binary.BigEndian.PutUint64(message[16:24], uint64(packet.PTS.Microseconds()))
	binary.BigEndian.PutUint32(message[24:28], uint32(packet.Width))
	binary.BigEndian.PutUint32(message[28:32], uint32(packet.Height))
	binary.BigEndian.PutUint32(message[32:36], uint32(len(packet.Data)))
	binary.BigEndian.PutUint32(message[36:40], configVersion)
	copy(message[videoHeaderSize:], packet.Data)
	return message, nil
}

func videoParameters(data []byte) []byte {
	var parameters []byte
	for len(data) > 0 {
		start := bytes.Index(data, []byte{0, 0, 1})
		if start < 0 || start+3 >= len(data) {
			break
		}
		data = data[start+3:]
		end := bytes.Index(data, []byte{0, 0, 1})
		if end < 0 {
			end = len(data)
		}
		if kind := data[0] & 31; kind == 7 || kind == 8 {
			parameters = append(parameters, bytes.TrimRight(data[:end], "\x00")...)
		}
		data = data[end:]
	}
	return parameters
}
