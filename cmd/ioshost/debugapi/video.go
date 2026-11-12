package debugapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"autogov1/v2/sdk/ios/video"
	"github.com/gorilla/websocket"
)

// VideoSource 只依赖 SDK 视频能力，测试时替换采集器，不伪造真机验证结果。
type VideoSource interface {
	Read(context.Context) (video.Packet, error)
	RequestKeyFrame(context.Context) error
	SetBitrate(context.Context, int) error
	Close() error
}

type Video struct {
	mu       sync.Mutex
	open     func(context.Context, video.Config) (VideoSource, error)
	run      *videoRun
	shutdown bool
	sockets  map[*websocket.Conn]struct{}
}

type videoRun struct {
	source             VideoSource
	cancel             context.CancelFunc
	done               chan struct{}
	packets            chan video.Packet
	latest             video.Packet
	started            time.Time
	ended              time.Time
	lastFrame          time.Time
	frames, size, keys uint64
	err                string
	closed             bool
	streaming          bool
}

func NewVideo(open func(context.Context, video.Config) (VideoSource, error)) *Video {
	if open == nil {
		open = func(ctx context.Context, cfg video.Config) (VideoSource, error) { return video.Start(ctx, cfg) }
	}
	return &Video{open: open, sockets: make(map[*websocket.Conn]struct{})}
}

type videoRequest struct {
	video.Config
	DurationSeconds int `json:"durationSeconds"`
}

func (v *Video) start(args map[string]any, streaming bool) (*videoRun, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	options := videoRequest{Config: video.Config{AutoRotation: true}}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&options); err != nil {
		return nil, err
	}
	if options.DurationSeconds == 0 {
		options.DurationSeconds = 30
	}
	if options.DurationSeconds < 1 || options.DurationSeconds > 120 {
		return nil, fmt.Errorf("视频调试时长必须为 1..120 秒")
	}
	if options.QueueBytes == 0 {
		options.QueueBytes = 1 << 20
	}
	if options.QueueBytes > 4<<20 {
		return nil, fmt.Errorf("调试视频缓冲不能超过 4 MiB")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.shutdown {
		return nil, fmt.Errorf("调试服务已关闭")
	}
	if v.run != nil && !v.run.closed {
		return nil, fmt.Errorf("已有视频调试会话，请先停止")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(options.DurationSeconds)*time.Second)
	source, err := v.open(ctx, options.Config)
	if err != nil {
		cancel()
		return nil, err
	}
	r := &videoRun{source: source, cancel: cancel, done: make(chan struct{}), packets: make(chan video.Packet, 4), started: time.Now(), streaming: streaming}
	v.run = r
	go v.pump(ctx, r)
	return r, nil
}

func (v *Video) pump(ctx context.Context, r *videoRun) {
	defer func() {
		r.cancel()
		err := r.source.Close()
		v.mu.Lock()
		if err != nil {
			r.err = "停止编码器失败：" + err.Error()
		} else {
			r.closed = true
		}
		r.ended = time.Now()
		close(r.done)
		v.mu.Unlock()
	}()
	for {
		packet, err := r.source.Read(ctx)
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, io.EOF) {
				v.mu.Lock()
				r.err = err.Error()
				v.mu.Unlock()
			}
			return
		}
		if len(packet.Data) == 0 {
			continue
		}
		if len(packet.Data) > 4<<20 {
			v.mu.Lock()
			r.err = "单帧超过调试上限 4 MiB"
			v.mu.Unlock()
			return
		}
		packet.Data = append([]byte(nil), packet.Data...)
		v.mu.Lock()
		r.latest = packet
		r.lastFrame = time.Now()
		r.frames++
		r.size += uint64(len(packet.Data))
		if packet.Keyframe {
			r.keys++
		}
		v.mu.Unlock()
		if r.streaming {
			// 下载端过慢时直接结束，不能丢掉参考帧后继续输出不可解码的 P 帧。
			select {
			case r.packets <- packet:
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
				v.mu.Lock()
				r.err = "视频下载端过慢，已停止采集"
				v.mu.Unlock()
				return
			}
		}
	}
}

func (v *Video) status() map[string]any {
	v.mu.Lock()
	defer v.mu.Unlock()
	r := v.run
	if r == nil {
		return map[string]any{"running": false}
	}
	end := r.ended
	if end.IsZero() {
		end = time.Now()
	}
	seconds := end.Sub(r.started).Seconds()
	age := int64(-1)
	if !r.lastFrame.IsZero() {
		age = time.Since(r.lastFrame).Milliseconds()
	}
	return map[string]any{"running": !r.closed, "frames": r.frames, "bytes": r.size, "keyframes": r.keys, "elapsedMs": int64(seconds * 1000), "lastFrameAgeMs": age, "averageFPS": float64(r.frames) / seconds, "averageBitrate": float64(r.size) * 8 / seconds, "width": r.latest.Width, "height": r.latest.Height, "ptsUs": r.latest.PTS.Microseconds(), "error": r.err, "streaming": r.streaming}
}

func (v *Video) stop(ctx context.Context, r *videoRun) error {
	if r == nil {
		return nil
	}
	r.cancel()
	select {
	case <-r.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if r.closed {
		return nil
	}
	// 清理失败时保留所有权，允许重试，不能启动第二个编码器。
	if err := r.source.Close(); err != nil {
		return err
	}
	r.closed = true
	return nil
}

func (v *Video) Close(ctx context.Context) error {
	v.mu.Lock()
	v.shutdown = true
	r := v.run
	sockets := make([]*websocket.Conn, 0, len(v.sockets))
	for conn := range v.sockets {
		sockets = append(sockets, conn)
	}
	v.mu.Unlock()
	// net/http.Shutdown 不负责已经升级的连接，必须显式关闭以唤醒读写协程。
	for _, conn := range sockets {
		_ = conn.Close()
	}
	return v.stop(ctx, r)
}

// call 的 read 返回最新帧的 JSON/base64；连续 Annex B 下载走专门的 HTTP 路径。
func (v *Video) call(ctx context.Context, method string, args map[string]any) (any, error) {
	if method == "video.status" {
		return v.status(), nil
	}
	if method == "video.start" {
		_, err := v.start(args, false)
		return v.status(), err
	}
	v.mu.Lock()
	r := v.run
	v.mu.Unlock()
	if method == "video.stop" {
		err := v.stop(ctx, r)
		return v.status(), err
	}
	if r == nil {
		return nil, fmt.Errorf("尚未启动视频调试")
	}
	switch method {
	case "video.keyframe":
		return map[string]any{"requested": true}, r.source.RequestKeyFrame(ctx)
	case "video.bitrate":
		value, ok := args["bitrate"].(float64)
		if !ok || value != float64(int(value)) || value < 64000 || value > 50000000 {
			return nil, fmt.Errorf("bitrate 必须是 64000..50000000 的整数")
		}
		return map[string]any{"bitrate": int(value)}, r.source.SetBitrate(ctx, int(value))
	case "video.read":
		v.mu.Lock()
		defer v.mu.Unlock()
		if len(r.latest.Data) == 0 {
			return nil, fmt.Errorf("编码器尚未输出首帧，请查看 video.status")
		}
		return map[string]any{"data": r.latest.Data, "sequence": r.frames, "ptsUs": r.latest.PTS.Microseconds(), "keyframe": r.latest.Keyframe, "width": r.latest.Width, "height": r.latest.Height, "mime": "video/h264"}, nil
	}
	return nil, fmt.Errorf("未知视频调试方法")
}

// stream 独占一次限时采集，HTTP 断开后释放；不抢占正在进行的手动调试。
func (v *Video) stream(w http.ResponseWriter, r *http.Request) {
	args := map[string]any{}
	for _, name := range []string{"durationSeconds", "maxSize", "fps", "bitrate"} {
		if value := r.URL.Query().Get(name); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil {
				failure(w, 400, "视频参数必须为整数")
				return
			}
			args[name] = n
		}
	}
	run, err := v.start(args, true)
	if err != nil {
		failure(w, 409, err.Error())
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_ = v.stop(ctx, run)
	}()
	controller := http.NewResponseController(w)
	started := false
	for {
		select {
		case packet := <-run.packets:
			if !started {
				// 文件必须从 SPS/PPS + IDR 开始，不能从中间的 P 帧起步。
				if !packet.Keyframe {
					continue
				}
				w.Header().Set("Content-Type", "video/h264")
				w.Header().Set("Content-Disposition", `attachment; filename="ios-debug.h264"`)
				w.Header().Set("X-Accel-Buffering", "no")
				started = true
			}
			_ = controller.SetWriteDeadline(time.Now().Add(3 * time.Second))
			if _, err = w.Write(packet.Data); err != nil {
				return
			}
			if err = controller.Flush(); err != nil {
				return
			}
		case <-run.done:
			if !started {
				failure(w, 409, "未获得可解码关键帧，请查看 /video/status")
			}
			return
		case <-r.Context().Done():
			return
		}
	}
}
