// Package yolo 对齐 AutoGo iOS 的目标检测 API（7 个函数）。
//
// 模型是 ncnn 格式（param + bin + labels）。AutoGo 的 New 直接把三样传进来，
// 说明模型文件是随项目走的（不是写死在 SDK 里）—— 我们也保持同样约定。
package yolo

import (
	"encoding/base64"
	"image"
	"time"

	"autogov1/v2/sdk/ios/files"
	"autogov1/v2/sdk/ios/images"
	"autogov1/v2/sdk/ios/internal/bridge"
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
type Yolo struct {
	version       string
	cpuThreadNum  int
	paramPath     string
	binPath       string
	labels        string
	closed        bool
}

// New 创建检测器。paramPath/binPath/labels 是磁盘路径；cpuThreadNum<=0 用原生默认。
func New(version string, cpuThreadNum int, paramPath, binPath, labels string) *Yolo {
	return &Yolo{version: version, cpuThreadNum: cpuThreadNum,
		paramPath: paramPath, binPath: binPath, labels: labels}
}

func (y *Yolo) ensure() bool {
	if y == nil || y.closed {
		return false
	}
	var out struct {
		OK bool `json:"ok"`
	}
	// 幂等加载：原生侧按 (param,bin) 键缓存，重复调用不会重复占内存。
	if !bridge.DoTimeout("yolo.load", map[string]any{
		"version": y.version, "threads": y.cpuThreadNum,
		"param": y.paramPath, "bin": y.binPath, "labels": y.labels,
	}, &out, 60*time.Second) {
		return false
	}
	return out.OK
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
	if !y.ensure() || img == nil {
		return
	}
	b64 := images.EncodeToBase64(img, "png", 100)
	if b64 == "" {
		return
	}
	_ = bridge.DoTimeout("yolo.set_image", map[string]any{"base64": b64}, &struct{}{}, 30*time.Second)
}

// Detect 在预置图（或当前屏幕）的指定区域内检测。
func (y *Yolo) Detect(x1, y1, x2, y2 int) []Result {
	if !y.ensure() {
		return nil
	}
	var out rawResult
	if !bridge.DoTimeout("yolo.detect", map[string]any{
		"region": []int{x1, y1, x2, y2},
	}, &out, 60*time.Second) {
		return nil
	}
	return finish(out.Items)
}

func (y *Yolo) DetectFromImage(img *image.NRGBA) []Result {
	if !y.ensure() || img == nil {
		return nil
	}
	b64 := images.EncodeToBase64(img, "png", 100)
	if b64 == "" {
		return nil
	}
	return y.DetectFromBase64(b64)
}

func (y *Yolo) DetectFromBase64(b64 string) []Result {
	if !y.ensure() || b64 == "" {
		return nil
	}
	var out rawResult
	if !bridge.DoTimeout("yolo.detect_image", map[string]any{"base64": b64}, &out, 60*time.Second) {
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
	if y == nil || y.closed {
		return
	}
	y.closed = true
	_ = bridge.Do("yolo.close", map[string]any{"param": y.paramPath, "bin": y.binPath}, nil)
}
