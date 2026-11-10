// Package dotocr 对齐 AutoGo iOS 的点阵 OCR（9 个函数）。
//
// 用途很专一：数字/符号的点阵识别（价格、数量、播放量那种等宽小字）。
// 它比 PP-OCR 快一个量级，因为不需要检测框，直接按阈值切字 + 字典匹配。
//
// 两类返回：
//   · Ocr*     → 整段文本（asJSON=true 时返回 JSON 串，与 AutoGo 一致）
//   · FindStr* → 目标字符串的左上角坐标，找不到返回 (-1,-1)
package dotocr

import (
	"encoding/base64"
	"image"
	"time"

	"autogov1/v2/sdk/ios/files"
	"autogov1/v2/sdk/ios/images"
	"autogov1/v2/sdk/ios/internal/bridge"
)

// SetDict 注册一套点阵字典（不同字体的数字模板）。dict 是原生侧的字典描述。
func SetDict(name, dict string) {
	_ = bridge.Do("dotocr.set_dict", map[string]any{"name": name, "dict": dict}, nil)
}

func ocrArgs(threshold string, sim float32, asJSON bool, dictName string) map[string]any {
	return map[string]any{"threshold": threshold, "sim": sim, "json": asJSON, "dict": dictName}
}

func ocrImage(img *image.NRGBA, threshold string, sim float32, asJSON bool, dictName string) string {
	if img == nil {
		return ""
	}
	b64 := images.EncodeToBase64(img, "png", 100)
	if b64 == "" {
		return ""
	}
	return ocrBase64(b64, threshold, sim, asJSON, dictName)
}

func ocrBase64(b64, threshold string, sim float32, asJSON bool, dictName string) string {
	args := ocrArgs(threshold, sim, asJSON, dictName)
	args["base64"] = b64
	var out struct {
		Text string `json:"text"`
	}
	if !bridge.DoTimeout("dotocr.ocr", args, &out, 30*time.Second) {
		return ""
	}
	return out.Text
}

// Ocr 截屏指定区域后识别。
func Ocr(x1, y1, x2, y2 int, threshold string, sim float32, asJSON bool, dictName string) string {
	return ocrImage(images.CaptureScreen(x1, y1, x2, y2), threshold, sim, asJSON, dictName)
}

func OcrFromImage(img *image.NRGBA, threshold string, sim float32, asJSON bool, dictName string) string {
	return ocrImage(img, threshold, sim, asJSON, dictName)
}

func OcrFromBase64(b64, threshold string, sim float32, asJSON bool, dictName string) string {
	if b64 == "" {
		return ""
	}
	return ocrBase64(b64, threshold, sim, asJSON, dictName)
}

func OcrFromPath(path, threshold string, sim float32, asJSON bool, dictName string) string {
	data := files.ReadBytes(path)
	if data == nil {
		return ""
	}
	return ocrBase64(base64.StdEncoding.EncodeToString(data), threshold, sim, asJSON, dictName)
}

func findStr(region []int, b64, text, threshold string, sim float32, dictName string) (int, int) {
	if b64 == "" || text == "" {
		return -1, -1
	}
	var out struct {
		X int `json:"x"`
		Y int `json:"y"`
	}
	args := map[string]any{"base64": b64, "text": text, "threshold": threshold, "sim": sim, "dict": dictName}
	if region != nil {
		args["region"] = region
	}
	if !bridge.DoTimeout("dotocr.find", args, &out, 30*time.Second) {
		return -1, -1
	}
	if out.X < 0 || out.Y < 0 {
		return -1, -1
	}
	return out.X, out.Y
}

func FindStr(x1, y1, x2, y2 int, text, threshold string, sim float32, dictName string) (int, int) {
	img := images.CaptureScreen(x1, y1, x2, y2)
	if img == nil {
		return -1, -1
	}
	b64 := images.EncodeToBase64(img, "png", 100)
	return findStr([]int{x1, y1, x2, y2}, b64, text, threshold, sim, dictName)
}

func FindStrFromImage(img *image.NRGBA, text, threshold string, sim float32, dictName string) (int, int) {
	if img == nil {
		return -1, -1
	}
	b64 := images.EncodeToBase64(img, "png", 100)
	return findStr(nil, b64, text, threshold, sim, dictName)
}

func FindStrFromBase64(b64, text, threshold string, sim float32, dictName string) (int, int) {
	return findStr(nil, b64, text, threshold, sim, dictName)
}

func FindStrFromPath(path, text, threshold string, sim float32, dictName string) (int, int) {
	data := files.ReadBytes(path)
	if data == nil {
		return -1, -1
	}
	return findStr(nil, base64.StdEncoding.EncodeToString(data), text, threshold, sim, dictName)
}
