package imaging

// 图像处理链 —— 对标 AutoGo images 的功能点(Grayscale/Threshold/AdaptiveThreshold/Binarization),
// 实现闭源不可得,故只对齐【API 面 + 语义】,算法纯 Go 自写(和 color.go 同哲学,不碰 cgo)。
// 缓冲约定与本包一致:rgb 为紧凑 RGB(w*h*3,行主序,左上原点);gray 为单通道(w*h)。
// 阈值/二值类是逐像素点运算,只需 gray;自适应阈值是空间运算,需 w,h。

import "math"

// Grayscale 彩色 RGB → 灰度(单通道 w*h)。
// 权重取 ITU-R BT.601 luma(0.299R+0.587G+0.114B),与 OpenCV COLOR_RGB2GRAY 一致。
func Grayscale(rgb []byte, w, h int) []byte {
	n := w * h
	if len(rgb) < n*3 {
		return nil
	}
	gray := make([]byte, n)
	for i := 0; i < n; i++ {
		r := int(rgb[i*3])
		g := int(rgb[i*3+1])
		b := int(rgb[i*3+2])
		// 定点:round((299*r+587*g+114*b)/1000)
		gray[i] = byte((299*r + 587*g + 114*b + 500) / 1000)
	}
	return gray
}

// 阈值类型(对标 OpenCV / AutoGo 的 typ 取值)。
//
//	BINARY:     v>thresh ? maxVal : 0
//	BINARY_INV: v>thresh ? 0 : maxVal
//	TRUNC:      v>thresh ? thresh : v
//	TOZERO:     v>thresh ? v : 0
//	TOZERO_INV: v>thresh ? 0 : v
func applyThresholdPoint(v, thresh, maxVal int, typ string) byte {
	switch typ {
	case "BINARY_INV":
		if v > thresh {
			return 0
		}
		return byte(maxVal)
	case "TRUNC":
		if v > thresh {
			return byte(thresh)
		}
		return byte(v)
	case "TOZERO":
		if v > thresh {
			return byte(v)
		}
		return 0
	case "TOZERO_INV":
		if v > thresh {
			return 0
		}
		return byte(v)
	default: // BINARY
		if v > thresh {
			return byte(maxVal)
		}
		return 0
	}
}

// Threshold 固定阈值化(输入单通道 gray;typ 见上)。返回新 gray,不改原图。
func Threshold(gray []byte, thresh, maxVal int, typ string) []byte {
	out := make([]byte, len(gray))
	for i, v := range gray {
		out[i] = applyThresholdPoint(int(v), thresh, maxVal, typ)
	}
	return out
}

// Binarize 二值化:>threshold → 255,否则 0(= Threshold 的 BINARY + maxVal 255,AutoGo 便捷版)。
func Binarize(gray []byte, threshold int) []byte {
	return Threshold(gray, threshold, 255, "BINARY")
}

// AdaptiveThreshold 自适应阈值化(输入单通道 gray)。
//
//	method: "MEAN_C"(邻域均值)| "GAUSSIAN_C"(高斯加权均值);
//	typ:    "BINARY" | "BINARY_INV";
//	blockSize: 邻域边长(奇数,<3 或偶数会自动纠正为最近的合法奇数);
//	c: 从局部阈值中减去的常数(越大越"黑")。
//
// 每像素阈值 T = 局部均值 - c;再按 typ 二值到 maxValue。
// MEAN_C 用积分图 O(1)/像素;GAUSSIAN_C 用可分离高斯核。边界一律 clamp。
func AdaptiveThreshold(gray []byte, w, h, maxValue int, method, typ string, blockSize, c int) []byte {
	if w <= 0 || h <= 0 || len(gray) < w*h {
		return nil
	}
	if blockSize < 3 {
		blockSize = 3
	}
	if blockSize%2 == 0 {
		blockSize++ // 必须奇数
	}
	r := blockSize / 2
	var localMean []int
	if method == "GAUSSIAN_C" {
		localMean = gaussianMean(gray, w, h, r)
	} else {
		localMean = boxMean(gray, w, h, r) // MEAN_C
	}
	out := make([]byte, w*h)
	inv := typ == "BINARY_INV"
	for i := 0; i < w*h; i++ {
		t := localMean[i] - c
		over := int(gray[i]) > t
		if over != inv { // BINARY: over→max;BINARY_INV: !over→max
			out[i] = byte(maxValue)
		} else {
			out[i] = 0
		}
	}
	return out
}

// boxMean 用积分图算每像素 (2r+1)^2 邻域均值(边界 clamp,窗口按实际像素数取平均)。
func boxMean(gray []byte, w, h, r int) []int {
	// 积分图 (w+1)*(h+1),int 足够(255*4096*4096 < 2^63)。
	sat := make([]int, (w+1)*(h+1))
	for y := 0; y < h; y++ {
		rowSum := 0
		for x := 0; x < w; x++ {
			rowSum += int(gray[y*w+x])
			sat[(y+1)*(w+1)+(x+1)] = sat[y*(w+1)+(x+1)] + rowSum
		}
	}
	sumRect := func(x1, y1, x2, y2 int) int { // 闭区间 [x1,x2]x[y1,y2]
		return sat[(y2+1)*(w+1)+(x2+1)] - sat[y1*(w+1)+(x2+1)] - sat[(y2+1)*(w+1)+x1] + sat[y1*(w+1)+x1]
	}
	out := make([]int, w*h)
	for y := 0; y < h; y++ {
		y1 := clamp(y-r, 0, h-1)
		y2 := clamp(y+r, 0, h-1)
		for x := 0; x < w; x++ {
			x1 := clamp(x-r, 0, w-1)
			x2 := clamp(x+r, 0, w-1)
			cnt := (x2 - x1 + 1) * (y2 - y1 + 1)
			out[y*w+x] = (sumRect(x1, y1, x2, y2) + cnt/2) / cnt
		}
	}
	return out
}

// gaussianMean 可分离高斯加权均值(sigma 按 OpenCV 经验:0.3*(r-1)+0.8,r=blockSize/2)。
func gaussianMean(gray []byte, w, h, r int) []int {
	sigma := 0.3*(float64(r)-1) + 0.8
	if sigma <= 0 {
		sigma = 0.8
	}
	// 一维核(2r+1),归一化
	k := make([]float64, 2*r+1)
	var ksum float64
	for i := -r; i <= r; i++ {
		x := float64(i)
		v := math.Exp(-(x * x) / (2 * sigma * sigma))
		k[i+r] = v
		ksum += v
	}
	for i := range k {
		k[i] /= ksum
	}
	// 横向卷积 → tmp(float),边界 clamp
	tmp := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var acc float64
			for i := -r; i <= r; i++ {
				acc += float64(gray[y*w+clamp(x+i, 0, w-1)]) * k[i+r]
			}
			tmp[y*w+x] = acc
		}
	}
	// 纵向卷积 → out(int,四舍五入)
	out := make([]int, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var acc float64
			for i := -r; i <= r; i++ {
				acc += tmp[clamp(y+i, 0, h-1)*w+x] * k[i+r]
			}
			out[y*w+x] = int(acc + 0.5)
		}
	}
	return out
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
