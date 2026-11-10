package images

import (
	"image"
	"image/color"
	"testing"
)

// 造一张 4x2 的图：左半红、右半蓝，用来测纯算法部分。
func testImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			if x < 2 {
				img.Set(x, y, color.NRGBA{R: 255, A: 255})
			} else {
				img.Set(x, y, color.NRGBA{B: 255, A: 255})
			}
		}
	}
	return img
}

func TestGrayscaleAndThreshold(t *testing.T) {
	img := testImage()
	gray := Grayscale(img)
	if gray == nil {
		t.Fatal("Grayscale 返回 nil")
	}
	if b := gray.Bounds(); b.Dx() != 4 || b.Dy() != 2 {
		t.Fatalf("灰度图尺寸 = %v", b)
	}
	// 纯红 → 灰度约 76（0.299*255）
	if v := gray.GrayAt(0, 0).Y; v < 70 || v > 82 {
		t.Fatalf("红色灰度 = %d，期望 ~76", v)
	}
	// 二值化（AutoGo 的命名是 ApplyBinarization）：高阈值把红色压成 0
	bin := ApplyBinarization(img, 200)
	if bin == nil {
		t.Fatal("ApplyBinarization 返回 nil")
	}
	if v := bin.GrayAt(0, 0).Y; v != 0 {
		t.Fatalf("阈值 200 时红色应为 0，实际 %d", v)
	}
	// 低阈值应保留白色/亮色
	bin2 := ApplyBinarization(img, 10)
	if v := bin2.GrayAt(0, 0).Y; v != 255 {
		t.Fatalf("阈值 10 时红色应被拉成 255，实际 %d", v)
	}
}

func TestClip(t *testing.T) {
	img := testImage()
	clipped := Clip(img, 1, 0, 3, 2)
	if clipped == nil {
		t.Fatal("Clip 返回 nil")
	}
	if b := clipped.Bounds(); b.Dx() != 2 || b.Dy() != 2 {
		t.Fatalf("裁剪尺寸 = %v", b)
	}
	// 裁剪后第一列来自原图 x=1（红），第二列来自 x=2（蓝）
	if c := clipped.NRGBAAt(0, 0); c.R != 255 {
		t.Fatalf("裁剪后左列应为红，实际 %v", c)
	}
	if c := clipped.NRGBAAt(1, 0); c.B != 255 {
		t.Fatalf("裁剪后右列应为蓝，实际 %v", c)
	}
}

// 越界的裁剪请求应被夹紧，而不是 panic 或返回错尺寸。
func TestClipOutOfRange(t *testing.T) {
	img := testImage()
	clipped := Clip(img, -5, -5, 100, 100)
	if clipped == nil {
		t.Fatal("越界裁剪应返回整图而不是 nil")
	}
	if b := clipped.Bounds(); b.Dx() != 4 || b.Dy() != 2 {
		t.Fatalf("越界裁剪尺寸 = %v", b)
	}
}
