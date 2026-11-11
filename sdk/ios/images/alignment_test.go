package images

import (
	"image"
	"image/color"
	"testing"
)

func TestResizePreservesAspectAndResamples(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	source.SetNRGBA(0, 0, color.NRGBA{A: 255})
	source.SetNRGBA(1, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	resized := Resize(source, 1, 0)
	if resized == nil || resized.Bounds() != image.Rect(0, 0, 1, 1) {
		t.Fatalf("按比例缩放失败: %v", resized)
	}
	value := resized.NRGBAAt(0, 0)
	if value.R < 120 || value.R > 135 || value.A != 255 {
		t.Fatalf("没有按 Lanczos 混合两个像素: %v", value)
	}
}

func TestRotateKeepsArbitraryAnglesAndClockwiseDirection(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	source.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 255})
	source.SetNRGBA(2, 0, color.NRGBA{B: 255, A: 255})
	right := Rotate(source, 90)
	if right.Bounds() != image.Rect(0, 0, 1, 3) || right.NRGBAAt(0, 0).R != 255 || right.NRGBAAt(0, 2).B != 255 {
		t.Fatal("顺时针旋转方向错误")
	}
	diagonal := Rotate(source, 45)
	if diagonal.Bounds().Dy() <= 1 || diagonal.Bounds().Dx() <= 1 {
		t.Fatalf("任意角度被截断: %v", diagonal.Bounds())
	}
}

func TestGrayUsesAlphaAndPreservesBounds(t *testing.T) {
	source := image.NewNRGBA(image.Rect(4, 5, 5, 6))
	source.SetNRGBA(4, 5, color.NRGBA{R: 255, A: 128})
	gray := Grayscale(source)
	if gray.Bounds() != source.Bounds() || gray.GrayAt(4, 5).Y != 38 {
		t.Fatalf("透明度或原点处理错误: %v/%v", gray.Bounds(), gray.GrayAt(4, 5))
	}
	if value := ApplyThreshold(source, 38, 255, "BINARY").GrayAt(4, 5).Y; value != 0 {
		t.Fatalf("相等阈值应归零: %d", value)
	}
	if value := ApplyThreshold(source, 38, 255, "BINARY_INV").GrayAt(4, 5).Y; value != 255 {
		t.Fatalf("反向阈值边界错误: %d", value)
	}
	if value := ApplyThreshold(source, 0, 255, "UNKNOWN").GrayAt(4, 5).Y; value != 38 {
		t.Fatalf("未知类型应保留灰度: %d", value)
	}
}

func TestAdaptiveReferenceBorderAndFractionalConstant(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 100, G: 100, B: 100, A: 255})
	if value := ApplyAdaptiveThreshold(source, 255, "MEAN_C", "BINARY", 3, 0).GrayAt(0, 0).Y; value != 255 {
		t.Fatalf("边界均值分母应保留窗口面积: %d", value)
	}
	if value := ApplyAdaptiveThreshold(source, 255, "GAUSSIAN_C", "BINARY", 3, 0).GrayAt(0, 0).Y; value != 0 {
		t.Fatalf("高斯有效权重归一化错误: %d", value)
	}
	if value := ApplyAdaptiveThreshold(source, 255, "GAUSSIAN_C", "BINARY", 3, 0.25).GrayAt(0, 0).Y; value != 255 {
		t.Fatalf("浮点 C 被截断: %d", value)
	}
}
