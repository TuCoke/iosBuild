package imaging

import (
	"fmt"
	"strconv"
	"strings"
)

// 找色/比色/多点比色 —— 对标 AutoGo images 的 API 面与格式(实现闭源,只对标 API+行为,算法自写)。
// rgb 为紧凑 RGB(w*h*3,来自 capture.ScreencapRGB),行主序,左上原点。
//
// 颜色串格式(照 AutoGo):
//   单色:  "RRGGBB" 或 "RRGGBB-偏色"(偏色为每通道允许误差的 16 进制,如 "-101010")。
//   多选:  "FFFFFF|CCCCCC-101010"( "|" 分隔的若干候选,命中任一即算匹配)。
//   sim:   相似度 0.1-1.0,越大越严。实际每通道容差 = 偏色 + round((1-sim)*255)(封 255)。
//   dir:   查找方向 0=左→右上→下 1=右→左上→下 2=左→右下→上 3=右→左下→上。

type colorSpec struct{ r, g, b, dr, dg, db int } // 基色 + 每通道偏色

func hex2(s string) int { v, _ := strconv.ParseInt(s, 16, 32); return int(v) }

// parseSpec 解析 "RRGGBB" 或 "RRGGBB-oooooo"。
func parseSpec(s string) (colorSpec, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	base, off := s, ""
	if i := strings.IndexByte(s, '-'); i >= 0 {
		base, off = s[:i], s[i+1:]
	}
	if len(base) != 6 {
		return colorSpec{}, fmt.Errorf("颜色需 RRGGBB: %q", s)
	}
	cs := colorSpec{r: hex2(base[0:2]), g: hex2(base[2:4]), b: hex2(base[4:6])}
	if len(off) == 6 {
		cs.dr, cs.dg, cs.db = hex2(off[0:2]), hex2(off[2:4]), hex2(off[4:6])
	}
	return cs, nil
}

// parseList 解析 "|" 分隔的候选颜色。
func parseList(s string) ([]colorSpec, error) {
	var out []colorSpec
	for _, part := range strings.Split(s, "|") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		cs, err := parseSpec(part)
		if err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("空颜色串")
	}
	return out, nil
}

func iabs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// matchPx 像素(索引 i)是否命中任一候选色。base = round((1-sim)*255)。
func matchPx(rgb []byte, i int, specs []colorSpec, base int) bool {
	R, G, B := int(rgb[i]), int(rgb[i+1]), int(rgb[i+2])
	for _, c := range specs {
		if iabs(R-c.r) <= c.dr+base && iabs(G-c.g) <= c.dg+base && iabs(B-c.b) <= c.db+base {
			return true
		}
	}
	return false
}

func simBase(sim float64) int {
	if sim <= 0 {
		sim = 0.9
	}
	b := int((1.0-sim)*255.0 + 0.5)
	if b < 0 {
		b = 0
	}
	if b > 255 {
		b = 255
	}
	return b
}

func clampRegion(w, h int, x1, y1, x2, y2 int) (int, int, int, int) {
	if x1 < 0 {
		x1 = 0
	}
	if y1 < 0 {
		y1 = 0
	}
	if x2 <= 0 || x2 > w {
		x2 = w
	}
	if y2 <= 0 || y2 > h {
		y2 = h
	}
	return x1, y1, x2, y2
}

// Pixel 取 (x,y) 颜色 "RRGGBB"。
func Pixel(rgb []byte, w, h, x, y int) string {
	if x < 0 || y < 0 || x >= w || y >= h {
		return ""
	}
	i := (y*w + x) * 3
	return fmt.Sprintf("%02X%02X%02X", rgb[i], rgb[i+1], rgb[i+2])
}

// CmpColor 判断 (x,y) 是否命中 colorStr(支持 | 多选、- 偏色)。
func CmpColor(rgb []byte, w, h, x, y int, colorStr string, sim float64) bool {
	if x < 0 || y < 0 || x >= w || y >= h {
		return false
	}
	specs, err := parseList(colorStr)
	if err != nil {
		return false
	}
	return matchPx(rgb, (y*w+x)*3, specs, simBase(sim))
}

// iterRegion 按 dir 遍历区域,命中回调返回 true 则停。
func iterRegion(w, x1, y1, x2, y2, dir int, fn func(x, y, i int) bool) (int, int, bool) {
	ys, ye, ystep := y1, y2, 1
	xs, xe, xstep := x1, x2, 1
	if dir == 2 || dir == 3 { // 下→上
		ys, ye, ystep = y2-1, y1-1, -1
	}
	if dir == 1 || dir == 3 { // 右→左
		xs, xe, xstep = x2-1, x1-1, -1
	}
	for y := ys; y != ye; y += ystep {
		row := y * w * 3
		for x := xs; x != xe; x += xstep {
			if fn(x, y, row+x*3) {
				return x, y, true
			}
		}
	}
	return -1, -1, false
}

// FindColor 区域内按 dir 找首个命中像素,返回坐标,找不到 (-1,-1)。
func FindColor(rgb []byte, w, h, x1, y1, x2, y2 int, colorStr string, sim, _dir float64) (int, int) {
	specs, err := parseList(colorStr)
	if err != nil {
		return -1, -1
	}
	dir := int(_dir)
	base := simBase(sim)
	x1, y1, x2, y2 = clampRegion(w, h, x1, y1, x2, y2)
	x, y, _ := iterRegion(w, x1, y1, x2, y2, dir, func(_, _, i int) bool { return matchPx(rgb, i, specs, base) })
	return x, y
}

// GetColorCountInRegion 区域内命中像素计数。
func GetColorCountInRegion(rgb []byte, w, h, x1, y1, x2, y2 int, colorStr string, sim float64) int {
	specs, err := parseList(colorStr)
	if err != nil {
		return 0
	}
	base := simBase(sim)
	x1, y1, x2, y2 = clampRegion(w, h, x1, y1, x2, y2)
	cnt := 0
	for y := y1; y < y2; y++ {
		row := y * w * 3
		for x := x1; x < x2; x++ {
			if matchPx(rgb, row+x*3, specs, base) {
				cnt++
			}
		}
	}
	return cnt
}

// mcPoint 多点比色的一个点:绝对/相对坐标 + 颜色候选。
type mcPoint struct {
	x, y  int
	specs []colorSpec
}

// parseAbsMulti 解析 "x,y,color,x,y,color,..."(DetectsMultiColors 用,绝对坐标)。
func parseAbsMulti(s string) ([]mcPoint, error) {
	t := strings.Split(s, ",")
	if len(t)%3 != 0 || len(t) == 0 {
		return nil, fmt.Errorf("多点比色格式应为 x,y,color 三元组")
	}
	var pts []mcPoint
	for i := 0; i+2 < len(t); i += 3 {
		x, _ := strconv.Atoi(strings.TrimSpace(t[i]))
		y, _ := strconv.Atoi(strings.TrimSpace(t[i+1]))
		sp, err := parseList(strings.TrimSpace(t[i+2]))
		if err != nil {
			return nil, err
		}
		pts = append(pts, mcPoint{x, y, sp})
	}
	return pts, nil
}

// DetectsMultiColors 绝对多点比色:所有点都命中各自颜色 → true。
func DetectsMultiColors(rgb []byte, w, h int, colors string, sim float64) bool {
	pts, err := parseAbsMulti(colors)
	if err != nil {
		return false
	}
	base := simBase(sim)
	for _, p := range pts {
		if p.x < 0 || p.y < 0 || p.x >= w || p.y >= h {
			return false
		}
		if !matchPx(rgb, (p.y*w+p.x)*3, p.specs, base) {
			return false
		}
	}
	return true
}

// parseRelMulti 解析 "anchorColor,dx,dy,color,dx,dy,color,...":首为参考色,其后 (dx,dy,color) 三元组。
func parseRelMulti(s string) ([]colorSpec, []mcPoint, error) {
	t := strings.Split(s, ",")
	if len(t) < 1 || (len(t)-1)%3 != 0 {
		return nil, nil, fmt.Errorf("相对多点找色格式应为 anchorColor,(dx,dy,color)...")
	}
	anchor, err := parseList(strings.TrimSpace(t[0]))
	if err != nil {
		return nil, nil, err
	}
	var rel []mcPoint
	for i := 1; i+2 < len(t)+1 && i+2 < len(t); i += 3 {
		dx, _ := strconv.Atoi(strings.TrimSpace(t[i]))
		dy, _ := strconv.Atoi(strings.TrimSpace(t[i+1]))
		sp, e := parseList(strings.TrimSpace(t[i+2]))
		if e != nil {
			return nil, nil, e
		}
		rel = append(rel, mcPoint{dx, dy, sp})
	}
	return anchor, rel, nil
}

// FindMultiColors 相对多点找色:区域内按 dir 找参考色,且各相对偏移点都命中 → 返回参考点。
// FindMultiColorsAll 多点找色的"找全部"版(对标 AutoGo FindMultiColorsAll)。
// 返回区域内所有命中的锚点像素坐标(光栅顺序,不受 dir 影响)。无命中返回 nil。
func FindMultiColorsAll(rgb []byte, w, h, x1, y1, x2, y2 int, colors string, sim float64) [][2]int {
	anchor, rel, err := parseRelMulti(colors)
	if err != nil {
		return nil
	}
	base := simBase(sim)
	x1, y1, x2, y2 = clampRegion(w, h, x1, y1, x2, y2)
	var out [][2]int
	for py := y1; py <= y2; py++ {
		for px := x1; px <= x2; px++ {
			if !matchPx(rgb, (py*w+px)*3, anchor, base) {
				continue
			}
			ok := true
			for _, r := range rel {
				ax, ay := px+r.x, py+r.y
				if ax < 0 || ay < 0 || ax >= w || ay >= h || !matchPx(rgb, (ay*w+ax)*3, r.specs, base) {
					ok = false
					break
				}
			}
			if ok {
				out = append(out, [2]int{px, py})
			}
		}
	}
	return out
}

func FindMultiColors(rgb []byte, w, h, x1, y1, x2, y2 int, colors string, sim, _dir float64) (int, int) {
	anchor, rel, err := parseRelMulti(colors)
	if err != nil {
		return -1, -1
	}
	dir := int(_dir)
	base := simBase(sim)
	x1, y1, x2, y2 = clampRegion(w, h, x1, y1, x2, y2)
	x, y, _ := iterRegion(w, x1, y1, x2, y2, dir, func(px, py, i int) bool {
		if !matchPx(rgb, i, anchor, base) {
			return false
		}
		for _, r := range rel {
			ax, ay := px+r.x, py+r.y
			if ax < 0 || ay < 0 || ax >= w || ay >= h {
				return false
			}
			if !matchPx(rgb, (ay*w+ax)*3, r.specs, base) {
				return false
			}
		}
		return true
	})
	return x, y
}
