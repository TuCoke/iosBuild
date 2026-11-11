package opencv

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"runtime"
	"sync"

	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/types"
)

// Mat 持有原生注册表令牌，不把 cv::Mat 指针暴露给脚本。复制结构体不会复制图像，使用 Clone。
// 每个派生 Mat 都拥有独立像素，必须 Close；关闭原图不会使裁剪图悬空。
type Mat struct {
	mu    sync.Mutex
	id    string
	scope *Scope
}
type Info struct {
	Width    int   `json:"width"`
	Height   int   `json:"height"`
	Channels int   `json:"channels"`
	Depth    int   `json:"depth"`
	Bytes    int64 `json:"bytes"`
}
type Match struct {
	X      int     `json:"x"`
	Y      int     `json:"y"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Score  float64 `json:"score"`
}
type MatchOptions struct {
	Gray        bool    `json:"gray"`
	Transparent bool    `json:"transparent"`
	Threshold   float64 `json:"threshold"`
	Limit       int     `json:"limit"`
}

func receive(ctx context.Context, op string, args map[string]any) (*Mat, error) {
	var result struct {
		ID string `json:"id"`
	}
	if err := native.Call(ctx, "opencv."+op, args, &result); err != nil {
		return nil, err
	}
	if result.ID == "" {
		return nil, fmt.Errorf("%w: OpenCV 返回空令牌", types.ErrUnavailable)
	}
	m := &Mat{id: result.ID}
	// finalizer 只是兜底，正常脚本必须 defer Close，避免大图堆积到下一次 GC。
	runtime.SetFinalizer(m, func(m *Mat) { _ = m.Close() })
	return m, nil
}
func Decode(ctx context.Context, encoded []byte) (*Mat, error) {
	if len(encoded) == 0 || len(encoded) > 24<<20 {
		return nil, fmt.Errorf("%w: 图片大小无效", types.ErrInvalid)
	}
	info, _, err := image.DecodeConfig(bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	if !validSize(info.Width, info.Height) {
		return nil, fmt.Errorf("%w: 图片超过像素限制", types.ErrInvalid)
	}
	return receive(ctx, "decode", map[string]any{"base64": base64.StdEncoding.EncodeToString(encoded)})
}
func validSize(w, h int) bool {
	return w > 0 && h > 0 && w <= 8192 && h <= 8192 && int64(w)*int64(h) <= 8<<20
}

// FromImage 统一转换为无预乘 RGBA；Go 的像素内存不跨 C 调用借用。
func FromImage(ctx context.Context, img image.Image) (*Mat, error) {
	if img == nil || !validSize(img.Bounds().Dx(), img.Bounds().Dy()) {
		return nil, fmt.Errorf("%w: 图片尺寸无效", types.ErrInvalid)
	}
	b := img.Bounds()
	rgba := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	return receive(ctx, "rgba", map[string]any{"width": b.Dx(), "height": b.Dy(), "base64": base64.StdEncoding.EncodeToString(rgba.Pix)})
}
func (m *Mat) identifier() (string, error) {
	if m == nil {
		return "", types.ErrClosed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.id == "" {
		return "", types.ErrClosed
	}
	return m.id, nil
}
func (m *Mat) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.id == "" {
		return nil
	}
	if err := native.Call(context.Background(), "opencv.close", map[string]any{"id": m.id}, nil); err != nil {
		return err
	}
	m.id = ""
	runtime.SetFinalizer(m, nil)
	if m.scope != nil {
		m.scope.mu.Lock()
		delete(m.scope.mats, m)
		m.scope.mu.Unlock()
	}
	return nil
}
func (m *Mat) Info(ctx context.Context) (Info, error) {
	var out Info
	id, err := m.identifier()
	if err != nil {
		return out, err
	}
	err = native.Call(ctx, "opencv.info", map[string]any{"id": id}, &out)
	return out, err
}
func (m *Mat) transform(ctx context.Context, op string, args map[string]any) (*Mat, error) {
	id, err := m.identifier()
	if err != nil {
		return nil, err
	}
	args["id"] = id
	if m.scope != nil {
		if err := m.scope.check(); err != nil {
			return nil, err
		}
		return m.scope.retain(receive(ctx, op, args))
	}
	return receive(ctx, op, args)
}
func (m *Mat) Clone(ctx context.Context) (*Mat, error) {
	return m.transform(ctx, "clone", map[string]any{})
}
func (m *Mat) Crop(ctx context.Context, rect image.Rectangle) (*Mat, error) {
	return m.transform(ctx, "crop", map[string]any{"x": rect.Min.X, "y": rect.Min.Y, "width": rect.Dx(), "height": rect.Dy()})
}
func (m *Mat) Resize(ctx context.Context, width, height int) (*Mat, error) {
	if !validSize(width, height) {
		return nil, types.ErrInvalid
	}
	return m.transform(ctx, "resize", map[string]any{"width": width, "height": height})
}
func (m *Mat) Gray(ctx context.Context) (*Mat, error) {
	return m.transform(ctx, "gray", map[string]any{})
}

// Rotate 使用顺时针四分之一圈数，支持负数；结果拥有独立内存。
func (m *Mat) Rotate(ctx context.Context, turns int) (*Mat, error) {
	return m.transform(ctx, "rotate", map[string]any{"turns": (turns%4 + 4) % 4})
}
func (m *Mat) Threshold(ctx context.Context, value float64, inverse bool) (*Mat, error) {
	if !(value >= 0 && value <= 255) {
		return nil, types.ErrInvalid
	}
	return m.transform(ctx, "threshold", map[string]any{"value": value, "inverse": inverse})
}
func (m *Mat) GaussianBlur(ctx context.Context, kernel int, sigma float64) (*Mat, error) {
	if kernel < 1 || kernel > 99 || kernel%2 == 0 || !(sigma >= 0 && sigma <= 1000) {
		return nil, types.ErrInvalid
	}
	return m.transform(ctx, "blur", map[string]any{"kernel": kernel, "sigma": sigma})
}
func (m *Mat) Canny(ctx context.Context, low, high float64) (*Mat, error) {
	if !(low >= 0 && high >= low && high <= 65535) {
		return nil, types.ErrInvalid
	}
	return m.transform(ctx, "canny", map[string]any{"low": low, "high": high})
}
func (m *Mat) Encode(ctx context.Context, format string, quality int) ([]byte, error) {
	if format != "png" && format != "jpeg" {
		return nil, types.ErrInvalid
	}
	if quality < 1 || quality > 100 {
		return nil, types.ErrInvalid
	}
	id, err := m.identifier()
	if err != nil {
		return nil, err
	}
	var out struct {
		Base64 string `json:"base64"`
	}
	if err = native.Call(ctx, "opencv.encode", map[string]any{"id": id, "format": format, "quality": quality}, &out); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(out.Base64)
}

// FindImageAll 使用归一化平方差，相同图像得分为 1；透明模板忽略透明像素及左上角同色像素。
// 返回结果按得分排序，并按模板区域抑制重复匹配。无匹配返回空切片，环境错误明确返回 error。
func FindImageAll(ctx context.Context, source, template *Mat, options MatchOptions) ([]Match, error) {
	if !(options.Threshold > 0 && options.Threshold <= 1) || options.Limit < 1 || options.Limit > 1000 {
		return nil, types.ErrInvalid
	}
	id, err := source.identifier()
	if err != nil {
		return nil, err
	}
	target, err := template.identifier()
	if err != nil {
		return nil, err
	}
	var out struct {
		Matches []Match `json:"matches"`
	}
	err = native.Call(ctx, "opencv.match", map[string]any{"id": id, "template": target, "options": options}, &out)
	return out.Matches, err
}
func FindImage(ctx context.Context, source, template *Mat, options MatchOptions) (Match, bool, error) {
	options.Limit = 1
	found, err := FindImageAll(ctx, source, template, options)
	if err != nil || len(found) == 0 {
		return Match{}, false, err
	}
	return found[0], true, nil
}
