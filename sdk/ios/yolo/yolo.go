// Package yolo 对齐 AutoGo iOS 的目标检测 API（7 个函数）。
//
// 模型是 ncnn 格式（param + bin + labels）。AutoGo 的 New 直接把三样传进来，
// 说明模型文件是随项目走的（不是写死在 SDK 里）—— 我们也保持同样约定。
package yolo

import (
	"context"
	"encoding/base64"
	"image"
	"sync"
	"sync/atomic"
	"time"

	"autogov1/v2/sdk/ios/files"
	"autogov1/v2/sdk/ios/images"
	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/types"
)

// Result 是一个检测框。
type Result struct {
	X       int     `json:"X"`
	Y       int     `json:"Y"`
	Width   int     `json:"宽"`
	Height  int     `json:"高"`
	Label   string  `json:"标签"`
	Score   float64 `json:"精度"`
	CenterX int     `json:"-"`
	CenterY int     `json:"-"`
}

// Yolo 是一个检测器实例。
var sequence atomic.Uint64

type Yolo struct {
	mu           sync.Mutex
	token        uint64
	loaded       bool
	lastErr      error
	preset       *image.NRGBA
	version      string
	cpuThreadNum int
	paramPath    string
	binPath      string
	labels       string
	closed       bool
}

// New 创建检测器。paramPath/binPath 是模型文件路径，labels 是标签内容；cpuThreadNum<=0 用原生默认。
func New(version string, cpuThreadNum int, paramPath, binPath, labels string) *Yolo {
	return &Yolo{token: sequence.Add(1), version: version, cpuThreadNum: cpuThreadNum,
		paramPath: paramPath, binPath: binPath, labels: labels}
}

// ensureLocked 将真实加载结果绑定到实例，不使用模型路径作为跨调用者共享句柄。
func (y *Yolo) ensureLocked(ctx context.Context) bool {
	if y.closed {
		y.lastErr = types.ErrClosed
		return false
	}
	if y.loaded {
		return true
	}
	y.lastErr = native.Call(ctx, "yolo.load", map[string]any{"token": y.token, "version": y.version, "threads": y.cpuThreadNum, "param": y.paramPath, "bin": y.binPath, "labels": y.labels}, nil)
	y.loaded = y.lastErr == nil
	return y.loaded
}
func (y *Yolo) Error() error {
	if y == nil {
		return types.ErrClosed
	}
	y.mu.Lock()
	defer y.mu.Unlock()
	return y.lastErr
}

type rawResult struct {
	Items []Result `json:"items"`
}

func finish(items []Result) []Result {
	for i := range items {
		items[i].CenterX = items[i].X + items[i].Width/2
		items[i].CenterY = items[i].Y + items[i].Height/2
	}
	return items
}

// SetImage 预置一张图，后续 Detect 直接用它（AutoGo 的用法：先 SetImage 再按区域反复检测）。
func (y *Yolo) SetImage(img *image.NRGBA) {
	if y == nil || img == nil {
		return
	}
	b64 := images.EncodeToBase64(img, "png", 100)
	if b64 == "" {
		return
	}
	y.mu.Lock()
	defer y.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if !y.ensureLocked(ctx) {
		return
	}
	y.lastErr = native.Call(ctx, "yolo.set_image", map[string]any{"token": y.token, "base64": b64}, nil)
	if y.lastErr == nil {
		y.preset = images.ReadFromBytes(mustDecode(b64))
	}
}
func mustDecode(value string) []byte { b, _ := base64.StdEncoding.DecodeString(value); return b }
func (y *Yolo) Detect(x1, y1, x2, y2 int) []Result {
	if y == nil {
		return nil
	}
	y.mu.Lock()
	preset := y.preset
	y.mu.Unlock()
	var img *image.NRGBA
	if preset == nil {
		img = images.CaptureScreen(x1, y1, x2, y2)
	} else {
		b := preset.Bounds()
		if x2 <= 0 {
			x2 = b.Max.X
		}
		if y2 <= 0 {
			y2 = b.Max.Y
		}
		img = images.Clip(preset, x1, y1, x2, y2)
	}
	items := y.DetectFromImage(img)
	for i := range items {
		items[i].X += max(x1, 0)
		items[i].Y += max(y1, 0)
	}
	return finish(items)
}
func (y *Yolo) DetectFromImage(img *image.NRGBA) []Result {
	if img == nil {
		return nil
	}
	items := y.DetectFromBase64(images.EncodeToBase64(img, "png", 100))
	for i := range items {
		items[i].X += img.Bounds().Min.X
		items[i].Y += img.Bounds().Min.Y
	}
	return finish(items)
}
func (y *Yolo) DetectFromBase64(b64 string) []Result {
	if y == nil || b64 == "" {
		return nil
	}
	y.mu.Lock()
	defer y.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if !y.ensureLocked(ctx) {
		return nil
	}
	var out rawResult
	y.lastErr = native.Call(ctx, "yolo.detect_image", map[string]any{"token": y.token, "base64": b64}, &out)
	if y.lastErr != nil {
		return nil
	}
	return finish(out.Items)
}

func (y *Yolo) DetectFromPath(path string) []Result {
	data := files.ReadBytes(path)
	if data == nil {
		return nil
	}
	return y.DetectFromBase64(base64.StdEncoding.EncodeToString(data))
}

// Close 释放模型。
func (y *Yolo) Close() {
	if y == nil {
		return
	}
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.closed {
		return
	}
	if y.loaded {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		y.lastErr = native.Call(ctx, "yolo.close", map[string]any{"token": y.token}, nil)
		if y.lastErr != nil {
			return
		}
	}
	y.closed = true
	y.loaded = false
	y.preset = nil
}
