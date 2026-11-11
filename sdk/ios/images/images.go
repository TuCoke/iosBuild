// Package images 对齐 AutoGo iOS 的图像 API（23 个函数，同名同参同返回）。
//
// 分工：
//
//	· 取图：CaptureScreen 走我们的 capture 包（CARenderServer + IOSurface）
//	· 算法：复用 autogov1/v2/sdk/imaging 的纯 Go 实现（找色/比色/多点/阈值/裁剪）
//	· 编解码：标准库 image/png、image/jpeg
//
// 样本 images 并非全部走 OpenCV；不能将识别引擎使用 OpenCV 与图像 API 的实现混为一谈。
package images

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"runtime"

	resample "github.com/disintegration/imaging"
	"image/png"
	"strings"
	"sync"
	"time"

	core "autogov1/v2/sdk/imaging"
	"autogov1/v2/sdk/ios/capture"
	"autogov1/v2/sdk/ios/files"
	"autogov1/v2/sdk/ios/internal/native"
)

// Point 是 FindMultiColorsAll 的返回项（物理像素坐标）。
type Point struct {
	X int
	Y int
}

// ---- 取图与缓存 ----
//
// 连续取图很贵（一次 CARenderServer 渲染 + IOSurface 读回）。
// Pixel/FindColor 这类高频调用若每次都取图，10 次调用就能把一个模板拖慢一倍，
// 所以缓存一帧，超过 frameTTL 才重新取。
const frameTTL = 200 * time.Millisecond

var (
	frameMu   sync.Mutex
	frameImg  *image.NRGBA
	frameAt   time.Time
	frameOnce sync.Once
)

// grab 取一帧（带缓存）。host 平台没有原生采集时返回 nil。
func grab() *image.NRGBA {
	frameOnce.Do(func() {})
	frameMu.Lock()
	defer frameMu.Unlock()
	if frameImg != nil && time.Since(frameAt) < frameTTL {
		return frameImg
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f, err := capture.Screenshot(ctx, capture.Options{AutoRotation: true})
	if err != nil || len(f.Data) == 0 {
		return nil
	}
	img, err := decode(f.Data)
	if err != nil {
		return nil
	}
	frameImg, frameAt = img, time.Now()
	return frameImg
}

// CaptureScreen 截屏并可裁剪；x2/y2 传 0 表示"到屏幕边缘"。
func CaptureScreen(x1, y1, x2, y2 int) *image.NRGBA {
	img := grab()
	if img == nil {
		return nil
	}
	b := img.Bounds()
	if x2 <= 0 || x2 > b.Dx() {
		x2 = b.Dx()
	}
	if y2 <= 0 || y2 > b.Dy() {
		y2 = b.Dy()
	}
	if x1 < 0 {
		x1 = 0
	}
	if y1 < 0 {
		y1 = 0
	}
	return Clip(img, x1, y1, x2, y2)
}

// ---- 回调式取图 ----
// 与参考实现一样使用原生连续采集；传 nil 停止。与 video.RawFrameStart 共享原始帧通道。
var cbMu sync.Mutex
var cbError error

func SetCallback(callback func(*image.NRGBA)) {
	err := native.RawStop()
	if err == nil && callback == nil {
		err = native.RawCallbackSet(nil)
	}
	if err == nil && callback != nil {
		err = native.RawCallbackSet(func(data []byte, w, h, stride int) {
			img := image.NewNRGBA(image.Rect(0, 0, w, h))
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					src := y*stride + x*4
					dst := y*img.Stride + x*4
					img.Pix[dst], img.Pix[dst+1], img.Pix[dst+2], img.Pix[dst+3] = data[src+2], data[src+1], data[src], 255
				}
			}
			frameMu.Lock()
			cached := image.NewNRGBA(img.Bounds())
			copy(cached.Pix, img.Pix)
			frameImg, frameAt = cached, time.Now()
			frameMu.Unlock()
			callback(img)
		})
		if err == nil {
			err = native.RawStart()
		}
	}
	cbMu.Lock()
	cbError = err
	cbMu.Unlock()
}
func CallbackError() error {
	cbMu.Lock()
	err := cbError
	cbMu.Unlock()
	if err != nil {
		return err
	}
	return native.CallbackError()
}

// ---- 像素与颜色 ----

// toRGB 把 NRGBA 拍平成 images 包要的 RGB 缓冲。
func toRGB(img *image.NRGBA) ([]byte, int, int) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(b.Min.X+x, b.Min.Y+y)
			o := (y*w + x) * 3
			out[o], out[o+1], out[o+2] = img.Pix[i], img.Pix[i+1], img.Pix[i+2]
		}
	}
	return out, w, h
}

// Pixel 返回该点颜色，格式 "RRGGBB"（大写十六进制）。
func Pixel(x, y int) string {
	img := grab()
	if img == nil {
		return ""
	}
	rgb, w, h := toRGB(img)
	if x < 0 || y < 0 || x >= w || y >= h {
		return ""
	}
	return core.Pixel(rgb, w, h, x, y)
}

// CmpColor 比较某点颜色；colorStr 支持 "FFFFFF|CCCCCC-101010" 多色或带偏色。
func CmpColor(x, y int, colorStr string, sim float32) bool {
	img := grab()
	if img == nil {
		return false
	}
	rgb, w, h := toRGB(img)
	return core.CmpColor(rgb, w, h, x, y, colorStr, float64(sim))
}

func FindColor(x1, y1, x2, y2 int, colorStr string, sim float32, dir int) (int, int) {
	img := grab()
	if img == nil {
		return -1, -1
	}
	rgb, w, h := toRGB(img)
	return core.FindColor(rgb, w, h, x1, y1, x2, y2, colorStr, float64(sim), float64(dir))
}

func GetColorCountInRegion(x1, y1, x2, y2 int, colorStr string, sim float32) int {
	img := grab()
	if img == nil {
		return 0
	}
	rgb, w, h := toRGB(img)
	return core.GetColorCountInRegion(rgb, w, h, x1, y1, x2, y2, colorStr, float64(sim))
}

func DetectsMultiColors(colors string, sim float32) bool {
	img := grab()
	if img == nil {
		return false
	}
	rgb, w, h := toRGB(img)
	return core.DetectsMultiColors(rgb, w, h, colors, float64(sim))
}

func FindMultiColors(x1, y1, x2, y2 int, colors string, sim float32, dir int) (int, int) {
	img := grab()
	if img == nil {
		return -1, -1
	}
	rgb, w, h := toRGB(img)
	return core.FindMultiColors(rgb, w, h, x1, y1, x2, y2, colors, float64(sim), float64(dir))
}

func FindMultiColorsAll(x1, y1, x2, y2 int, colors string, sim float32, dir int) []Point {
	img := grab()
	if img == nil {
		return nil
	}
	rgb, w, h := toRGB(img)
	raw := core.FindMultiColorsAll(rgb, w, h, x1, y1, x2, y2, colors, float64(sim))
	out := make([]Point, 0, len(raw))
	for _, p := range raw {
		out = append(out, Point{X: p[0], Y: p[1]})
	}
	return out
}

// ---- 编解码 ----

func decode(data []byte) (*image.NRGBA, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return ToNrgba(img), nil
}

func ReadFromBytes(data []byte) *image.NRGBA {
	img, err := decode(data)
	if err != nil {
		return nil
	}
	return img
}

func ReadFromPath(path string) *image.NRGBA {
	data := readFile(path)
	if data == nil {
		return nil
	}
	return ReadFromBytes(data)
}

func ReadFromBase64(base64Str string) *image.NRGBA {
	data, err := base64.StdEncoding.DecodeString(base64Str)
	if err != nil {
		// 兼容 URL-safe 与无填充的写法的服务端返回值
		if d2, e2 := base64.RawStdEncoding.DecodeString(base64Str); e2 == nil {
			data = d2
		} else if d3, e3 := base64.URLEncoding.DecodeString(base64Str); e3 == nil {
			data = d3
		} else {
			return nil
		}
	}
	return ReadFromBytes(data)
}

// EncodeToBytes 按 format 编码；quality 只对 jpg 生效（1~100）。
func EncodeToBytes(img *image.NRGBA, format string, quality int) []byte {
	if img == nil {
		return nil
	}
	var buf bytes.Buffer
	switch format {
	case "jpg", "jpeg", "JPG", "JPEG":
		q := quality
		if q <= 0 || q > 100 {
			q = 90
		}
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
			return nil
		}
	default:
		if err := png.Encode(&buf, img); err != nil {
			return nil
		}
	}
	return buf.Bytes()
}

func EncodeToBase64(img *image.NRGBA, format string, quality int) string {
	data := EncodeToBytes(img, format, quality)
	if data == nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(data)
}

// Save 把图片存到指定路径（有扩展名决定格式）。
func Save(img *image.NRGBA, path string, quality int) bool {
	if img == nil {
		return false
	}
	format := "png"
	if ext := lowerExt(path); ext == ".jpg" || ext == ".jpeg" {
		format = "jpg"
	}
	data := EncodeToBytes(img, format, quality)
	if data == nil {
		return false
	}
	return writeFile(path, data)
}

// ToNrgba 把任意 image.Image 转成 *image.NRGBA（裁剪到 bounds 内的有效像素）。
func ToNrgba(img image.Image) *image.NRGBA {
	if img == nil {
		return nil
	}
	if n, ok := img.(*image.NRGBA); ok {
		return n
	}
	b := img.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(x-b.Min.X, y-b.Min.Y, img.At(x, y))
		}
	}
	return dst
}

// ---- 变换 ----

func Clip(img *image.NRGBA, x1, y1, x2, y2 int) *image.NRGBA {
	if img == nil {
		return nil
	}
	b := img.Bounds()
	x1, y1 = clampInt(x1, 0, b.Dx()), clampInt(y1, 0, b.Dy())
	x2, y2 = clampInt(x2, 0, b.Dx()), clampInt(y2, 0, b.Dy())
	if x2 <= x1 || y2 <= y1 {
		return nil
	}
	dst := image.NewNRGBA(image.Rect(0, 0, x2-x1, y2-y1))
	for y := y1; y < y2; y++ {
		copy(dst.Pix[(y-y1)*dst.Stride:(y-y1)*dst.Stride+(x2-x1)*4], img.Pix[y*img.Stride+x1*4:y*img.Stride+x2*4])
	}
	return dst
}

// Resize 与样本相同使用 Lanczos（support=3）；一个维度为零时按比例计算。
func Resize(img *image.NRGBA, width, height int) *image.NRGBA {
	if img == nil || img.Bounds().Empty() || width < 0 || height < 0 || (width == 0 && height == 0) {
		return nil
	}
	return resample.Resize(img, width, height, resample.Lanczos)
}

// Rotate 接收顺时针角度；样本向 imaging.Rotate 传负角度，背景为透明 Alpha16(0)。
func Rotate(img *image.NRGBA, degree int) *image.NRGBA {
	if img == nil || img.Bounds().Empty() {
		return nil
	}
	return resample.Rotate(img, -float64(degree), color.Transparent)
}

// parallelRows 只并行处理不同输出行，避免按屏幕高度无限创建 goroutine。
func parallelRows(bounds image.Rectangle, work func(int)) {
	workers := min(runtime.GOMAXPROCS(0), bounds.Dy())
	if workers <= 0 {
		return
	}
	var group sync.WaitGroup
	group.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(start int) {
			defer group.Done()
			for y := bounds.Min.Y + start; y < bounds.Max.Y; y += workers {
				work(y)
			}
		}(worker)
	}
	group.Wait()
}

// Grayscale 走样本使用的 color.GrayModel，保留非零图像原点并正确处理 NRGBA 透明度。
func Grayscale(img *image.NRGBA) *image.Gray {
	if img == nil {
		return nil
	}
	bounds := img.Bounds()
	out := image.NewGray(bounds)
	parallelRows(bounds, func(y int) {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			out.SetGray(x, y, color.GrayModel.Convert(img.NRGBAAt(x, y)).(color.Gray))
		}
	})
	return out
}

func ApplyThreshold(img *image.NRGBA, threshold, maxVal int, typ string) *image.Gray {
	out := Grayscale(img)
	if out == nil {
		return nil
	}
	bounds := out.Bounds()
	parallelRows(bounds, func(y int) {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			value := int(out.GrayAt(x, y).Y)
			switch typ {
			case "BINARY":
				if value > threshold {
					value = maxVal
				} else {
					value = 0
				}
			case "BINARY_INV":
				if value > threshold {
					value = 0
				} else {
					value = maxVal
				}
			case "TRUNC":
				value = min(value, threshold)
			case "TOZERO":
				if value <= threshold {
					value = 0
				}
			case "TOZERO_INV":
				if value > threshold {
					value = 0
				}
				// 样本对未知类型保留原灰度，不静默替换成 BINARY。
			}
			out.SetGray(x, y, color.Gray{Y: uint8(value)})
		}
	})
	return out
}

// ApplyAdaptiveThreshold 对齐样本的浮点阈值与边界处理；blockSize 应为正奇数。
// MEAN_C 在边界外视为零，分母仍为 blockSize²；GAUSSIAN_C 按有效邻域权重归一化，sigma=blockSize。
func ApplyAdaptiveThreshold(img *image.NRGBA, maxValue float64, adaptiveMethod, thresholdType string, blockSize int, C float64) *image.Gray {
	if img == nil || blockSize <= 0 || math.IsNaN(maxValue) || math.IsInf(maxValue, 0) || math.IsNaN(C) || math.IsInf(C, 0) {
		return nil
	}
	gray := Grayscale(img)
	bounds := gray.Bounds()
	out := image.NewGray(bounds)
	radius := blockSize / 2
	// 窗口外的权重不会使用；限制预计算长度为实际可能命中的距离。
	rx, ry := min(radius, max(0, bounds.Dx()-1)), min(radius, max(0, bounds.Dy()-1))
	gaussian := adaptiveMethod == "GAUSSIAN_C"
	var wx, wy []float64
	if gaussian {
		wx = make([]float64, 2*rx+1)
		wy = make([]float64, 2*ry+1)
		sigma := float64(blockSize)
		for i := -rx; i <= rx; i++ {
			d := float64(i)
			wx[i+rx] = math.Exp(-d * d / (2 * sigma * sigma))
		}
		for i := -ry; i <= ry; i++ {
			d := float64(i)
			wy[i+ry] = math.Exp(-d * d / (2 * sigma * sigma))
		}
	}
	parallelRows(bounds, func(y int) {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			sum, weightSum := 0.0, 0.0
			for ny := max(bounds.Min.Y, y-ry); ny <= min(bounds.Max.Y-1, y+ry); ny++ {
				for nx := max(bounds.Min.X, x-rx); nx <= min(bounds.Max.X-1, x+rx); nx++ {
					weight := 1.0
					if gaussian {
						weight = wx[nx-x+rx] * wy[ny-y+ry]
					}
					sum += float64(gray.GrayAt(nx, ny).Y) * weight
					weightSum += weight
				}
			}
			if !gaussian {
				weightSum = float64(blockSize) * float64(blockSize)
			}
			threshold := sum/weightSum - C
			over := float64(gray.GrayAt(x, y).Y) > threshold
			value := uint8(0)
			if (thresholdType == "BINARY" && over) || (thresholdType == "BINARY_INV" && !over) {
				value = uint8(maxValue)
			}
			out.SetGray(x, y, color.Gray{Y: value})
		}
	})
	return out
}

func ApplyBinarization(img *image.NRGBA, threshold int) *image.Gray {
	return ApplyThreshold(img, threshold, 255, "BINARY")
}

// ---- 小工具 ----

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// 读写复用 files 包：同一份沙盒路径规则，避免两处各算一遍。
func readFile(p string) []byte { return files.ReadBytes(p) }

func writeFile(p string, data []byte) bool {
	files.WriteBytes(p, data)
	return files.Exists(p)
}

func lowerExt(p string) string { return "." + strings.ToLower(files.GetExtension(p)) }
