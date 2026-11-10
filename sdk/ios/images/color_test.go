package images

import (
	"image"
	"testing"
	"time"
)

// setFrame 直接往包级缓存里塞一帧，绕过原生采集。
//
// 这是同包测试的特权：颜色函数都走 grab()，而 grab() 优先用缓存，
// 所以只要把 frameImg/frameAt 填好，就能在没设备的环境里测真实算法。
func setFrame(img *image.NRGBA) {
	frameMu.Lock()
	defer frameMu.Unlock()
	frameImg = img
	frameAt = time.Now()
}

func TestCmpColor(t *testing.T) {
	setFrame(testImage()) // 左半红、右半蓝
	// 纯红/纯蓝，相似度 1.0 应精确命中
	if !CmpColor(0, 0, "FF0000", 1.0) {
		t.Fatal("(0,0) 应为红")
	}
	if CmpColor(0, 0, "0000FF", 1.0) {
		t.Fatal("(0,0) 不该是蓝")
	}
	if !CmpColor(2, 0, "0000FF", 1.0) {
		t.Fatal("(2,0) 应为蓝")
	}
	// 带偏色：FF0000-101010 允许每通道误差 0x10
	if !CmpColor(0, 0, "FE1010-101010", 1.0) {
		t.Fatal("带偏色应命中")
	}
	// 多候选用 | 分隔，命中任一即可
	if !CmpColor(0, 0, "00FF00|FF0000", 1.0) {
		t.Fatal("多选色应命中 FF0000")
	}
}

func TestFindColorAndCount(t *testing.T) {
	setFrame(testImage())
	// 从左上往右下找红色：应命中 (0,0)
	if x, y := FindColor(0, 0, 4, 2, "FF0000", 1.0, 0); x != 0 || y != 0 {
		t.Fatalf("FindColor 红 = (%d,%d)，期望 (0,0)", x, y)
	}
	// 找蓝色：应命中 x=2 的第一行
	if x, y := FindColor(0, 0, 4, 2, "0000FF", 1.0, 0); x != 2 || y != 0 {
		t.Fatalf("FindColor 蓝 = (%d,%d)，期望 (2,0)", x, y)
	}
	// 找不存在的颜色
	if x, y := FindColor(0, 0, 4, 2, "00FF00", 1.0, 0); x != -1 || y != -1 {
		t.Fatalf("找不到应返回 (-1,-1)，实际 (%d,%d)", x, y)
	}
	// 区域计数：整屏有 4 个红像素（4x2 的左半）
	if n := GetColorCountInRegion(0, 0, 4, 2, "FF0000", 1.0); n != 4 {
		t.Fatalf("红像素数 = %d，期望 4", n)
	}
}

func TestDetectsMultiColors(t *testing.T) {
	setFrame(testImage())
	// DetectsMultiColors 用【绝对坐标】三元组：x,y,color,x,y,color
	if !DetectsMultiColors("0,0,FF0000,2,0,0000FF", 1.0) {
		t.Fatal("多点比色应命中（(0,0) 红 + (2,0) 蓝）")
	}
	// 坐标对但颜色不对 → 不命中
	if DetectsMultiColors("0,0,FF0000,2,0,00FF00", 1.0) {
		t.Fatal("颜色不符不该命中")
	}
}


