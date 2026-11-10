// Package images 对齐 AutoGo iOS 的图像 API（23 个函数，同名同参同返回）。
//
// 分工：
//   · 取图：CaptureScreen 走我们的 capture 包（CARenderServer + IOSurface）
//   · 算法：复用 autogov1/v2/sdk/imaging 的纯 Go 实现（找色/比色/多点/阈值/裁剪）
//   · 编解码：标准库 image/png、image/jpeg
//
// 【为什么算法复用安卓侧那份】它没有任何平台依赖（只吃 []byte + 宽高），
// 而且已经在真机上被闲鱼/小红书模板用过；为 iOS 重写一份等于把踩过的坑再踩一遍。
package images

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"sync"
	"time"

	core "autogov1/v2/sdk/imaging"
	"autogov1/v2/sdk/ios/capture"
	"autogov1/v2/sdk/ios/files"
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
	f, err := capture.Screenshot(ctx, capture.Options{})
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
//
// AutoGo 的 SetCallback 是"新帧到达就回调"。我们没有原生的连续采集回调，
// 用固定节拍的拉取实现同样语义；传 nil 停止。
var (
	cbMu     sync.Mutex
	cbFunc   func(*image.NRGBA)
	cbStop   chan struct{}
	cbPeriod = 100 * time.Millisecond
)

func SetCallback(callback func(img *image.NRGBA)) {
	cbMu.Lock()
	defer cbMu.Unlock()
	if cbStop != nil {
		close(cbStop)
		cbStop = nil
	}
	cbFunc = callback
	if callback == nil {
		return
	}
	stop := make(chan struct{})
	cbStop = stop
	go func() {
		ticker := time.NewTicker(cbPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				cbMu.Lock()
				fn := cbFunc
				cbMu.Unlock()
				if fn == nil {
					return
				}
				// 缓存必须绕过：回调要的就是"新帧"。
				frameMu.Lock()
				frameImg, frameAt = nil, time.Time{}
				frameMu.Unlock()
				if img := grab(); img != nil {
					fn(img)
				}
			}
		}
	}()
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

// Resize 用最近邻缩放：够快，且不引入额外依赖（AutoGo 走 opencv，效果更好但重得多）。
func Resize(img *image.NRGBA, width, height int) *image.NRGBA {
	if img == nil || width <= 0 || height <= 0 {
		return nil
	}
	src := img.Bounds()
	sw, sh := src.Dx(), src.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		sy := y * sh / height
		for x := 0; x < width; x++ {
			sx := x * sw / width
			copy(dst.Pix[y*dst.Stride+x*4:y*dst.Stride+x*4+4], img.Pix[sy*img.Stride+sx*4:sy*img.Stride+sx*4+4])
		}
	}
	return dst
}

// Rotate 只支持 90 的整数倍（顺时针）；其它角度按最近的角度处理。
func Rotate(img *image.NRGBA, degree int) *image.NRGBA {
	if img == nil {
		return nil
	}
	d := ((degree % 360) + 360) % 360
	d = (d / 90) * 90
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	switch d {
	case 0:
		return img
	case 90, 270:
		dst := image.NewNRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var dx, dy int
				if d == 90 {
					dx, dy = h-1-y, x
				} else {
					dx, dy = y, w-1-x
				}
				copy(dst.Pix[dy*dst.Stride+dx*4:dy*dst.Stride+dx*4+4], img.Pix[y*img.Stride+x*4:y*img.Stride+x*4+4])
			}
		}
		return dst
	default: // 180
		dst := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dx, dy := w-1-x, h-1-y
				copy(dst.Pix[dy*dst.Stride+dx*4:dy*dst.Stride+dx*4+4], img.Pix[y*img.Stride+x*4:y*img.Stride+x*4+4])
			}
		}
		return dst
	}
}

// ---- 灰度与阈值 ----

func Grayscale(img *image.NRGBA) *image.Gray {
	if img == nil {
		return nil
	}
	rgb, w, h := toRGB(img)
	g := core.Grayscale(rgb, w, h)
	out := image.NewGray(image.Rect(0, 0, w, h))
	copy(out.Pix, g)
	return out
}

func ApplyThreshold(img *image.NRGBA, threshold, maxVal int, typ string) *image.Gray {
	if img == nil {
		return nil
	}
	gray := Grayscale(img)
	out := core.Threshold(gray.Pix, threshold, maxVal, typ)
	dst := image.NewGray(gray.Bounds())
	copy(dst.Pix, out)
	return dst
}

func ApplyAdaptiveThreshold(img *image.NRGBA, maxValue float64, adaptiveMethod, thresholdType string, blockSize int, C float64) *image.Gray {
	if img == nil {
		return nil
	}
	gray := Grayscale(img)
	b := gray.Bounds()
	out := core.AdaptiveThreshold(gray.Pix, b.Dx(), b.Dy(), int(maxValue), adaptiveMethod, thresholdType, blockSize, int(C))
	dst := image.NewGray(b)
	copy(dst.Pix, out)
	return dst
}

func ApplyBinarization(img *image.NRGBA, threshold int) *image.Gray {
	if img == nil {
		return nil
	}
	gray := Grayscale(img)
	out := core.Binarize(gray.Pix, threshold)
	dst := image.NewGray(gray.Bounds())
	copy(dst.Pix, out)
	return dst
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

func lowerExt(p string) string { return strings.ToLower(files.GetExtension(p)) }
