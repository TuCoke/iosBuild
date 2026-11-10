// Package ppocr 对齐 AutoGo iOS 的 OCR API（6 个函数）。
//
// 后端是 PP-OCRv5 mobile（det + rec）+ ncnn，全部在原生侧完成；
// Go 这层只负责：把图（屏幕 / NRGBA / base64 / 文件）交给原生，拿回结果。
//
// 【结果字段用中文 JSON 标签】AutoGo 的 Result 就是这么序列化的（`json:"宽"`），
// 保持一致才能让用户脚本里的字段名一字不差。
package ppocr

import (
	"encoding/base64"
	"image"
	"time"

	"autogov1/v2/sdk/ios/files"
	"autogov1/v2/sdk/ios/images"
	"autogov1/v2/sdk/ios/internal/bridge"
)

// Result 是一条识别结果（物理像素 + 文本 + 置信度 + 中心点）。
type Result struct {
	X       int     `json:"X"`
	Y       int     `json:"Y"`
	Width   int     `json:"宽"`
	Height  int     `json:"高"`
	Label   string  `json:"标签"`
	Score   float64 `json:"精度"`
	CenterX int     `json:"-"` // 中心坐标X
	CenterY int     `json:"-"` // 中心坐标Y
}

// Ppocr 是一个 OCR 引擎实例；Close 后不应再调用。
type Ppocr struct {
	version string
	closed  bool
}

// New 创建引擎。version 对应模型版本（如 "v5"），空串用原生默认。
func New(version string) *Ppocr { return &Ppocr{version: version} }

type rawResult struct {
	Items []Result `json:"items"`
}

// finish 补中心点：原生只给左上角与宽高，中心点统一在这里算，
// 避免两边各算一遍出现 1px 偏差。
func finish(items []Result) []Result {
	for i := range items {
		items[i].CenterX = items[i].X + items[i].Width/2
		items[i].CenterY = items[i].Y + items[i].Height/2
	}
	return items
}

// Ocr 截屏指定区域后识别；colorStr 是预处理用的颜色过滤（可空，x2/y2 传 0 表示整屏）。
func (p *Ppocr) Ocr(x1, y1, x2, y2 int, colorStr string) []Result {
	img := images.CaptureScreen(x1, y1, x2, y2)
	if img == nil {
		return nil
	}
	return p.OcrFromImage(img, colorStr)
}

func (p *Ppocr) OcrFromImage(img *image.NRGBA, colorStr string) []Result {
	if p == nil || p.closed || img == nil {
		return nil
	}
	b64 := images.EncodeToBase64(img, "png", 100)
	if b64 == "" {
		return nil
	}
	return p.OcrFromBase64(b64, colorStr)
}

func (p *Ppocr) OcrFromBase64(b64 string, colorStr string) []Result {
	if p == nil || p.closed || b64 == "" {
		return nil
	}
	var out rawResult
	if !bridge.DoTimeout("ppocr.ocr", map[string]any{
		"version": p.version, "base64": b64, "color": colorStr,
	}, &out, 60*time.Second) {
		return nil
	}
	return finish(out.Items)
}

func (p *Ppocr) OcrFromPath(path string, colorStr string) []Result {
	data := files.ReadBytes(path)
	if data == nil {
		return nil
	}
	return p.OcrFromBase64(base64.StdEncoding.EncodeToString(data), colorStr)
}

// Close 释放原生引擎（模型常驻几十 MB，用完要还）。
func (p *Ppocr) Close() {
	if p == nil || p.closed {
		return
	}
	p.closed = true
	_ = bridge.Do("ppocr.close", map[string]any{"version": p.version}, nil)
}
