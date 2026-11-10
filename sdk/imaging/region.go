package imaging

// CropGray 从灰度屏(w×h,行主序)裁出像素区域 [x1,y1,x2,y2) 的新灰度缓冲。
// 整屏(区域==全屏)时零拷贝直接返回原缓冲。调用方须保证区域已夹紧合法。
func CropGray(gray []byte, w, h, x1, y1, x2, y2 int) ([]byte, int, int) {
	if x1 == 0 && y1 == 0 && x2 == w && y2 == h {
		return gray, w, h
	}
	cw, ch := x2-x1, y2-y1
	out := make([]byte, cw*ch)
	for y := 0; y < ch; y++ {
		srcOff := (y1+y)*w + x1
		copy(out[y*cw:(y+1)*cw], gray[srcOff:srcOff+cw])
	}
	return out, cw, ch
}

// FindImageRegion 在屏灰度的指定像素区域内找图,命中坐标换算回全帧(改进#4)。
// x2/y2<=0 或越界表示到边界;区域非法/等于全屏则退化为整屏 FindImage。
