package protocol

import "strings"

const ActionAcknowledgmentV1 = "action_ack_v1"

// NormalizeFeatures 限制注册元数据体积并去重，未知的新能力可以保留以便协议演进。
func NormalizeFeatures(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		if len(out) >= 32 {
			break
		}
		if value == "" || len(value) > 64 || strings.ContainsAny(value, "\x00\r\n\t ") || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// RequiresActionAcknowledgment 标记旧端不返回执行回执的动作；新端必须显式声明支持。
func RequiresActionAcknowledgment(command string) bool {
	switch command {
	case "tap", "touch", "swipe", "key", "long_press", "pinch", "gesture", "input_reset", "wake", "sleep":
		return true
	default:
		return false
	}
}
