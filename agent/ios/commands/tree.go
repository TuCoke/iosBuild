package commands

import "context"

// 保持云端既有 ui_result 坐标约定；iOS AX 的物理像素只在适配边界归一化。
func (c *Driver) Tree(ctx context.Context) ([]map[string]any, error) {
	info, e := c.Info(ctx)
	if e != nil {
		return nil, e
	}
	var result struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if e = c.Invoke(ctx, "uiacc.snapshot", map[string]any{}, &result); e != nil {
		return nil, e
	}
	w, h := float64(info.Display.Width), float64(info.Display.Height)
	if info.Display.Rotation%2 != 0 {
		w, h = h, w
	}
	out := make([]map[string]any, 0, len(result.Nodes))
	for _, n := range result.Nodes {
		left, _ := n["left"].(float64)
		top, _ := n["top"].(float64)
		right, _ := n["right"].(float64)
		bottom, _ := n["bottom"].(float64)
		n["id"] = n["identifier"]
		n["desc"] = n["value"]
		n["x"] = left / w
		n["y"] = top / h
		n["w"] = (right - left) / w
		n["h"] = (bottom - top) / h
		n["cx"] = (left + right) / 2 / w
		n["cy"] = (top + bottom) / 2 / h
		for _, key := range []string{"clickable", "scrollable", "checkable", "checked", "editable", "selected", "focused", "enabled", "password"} {
			value := n[key]
			n[key] = value == true || value == float64(1)
		}
		out = append(out, n)
	}
	return out, nil
}
