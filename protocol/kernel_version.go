package protocol

import (
	"fmt"
	"golang.org/x/mod/semver"
	"strings"
	"time"
)

// CompareKernelVersions 支持固定时间版本和语义版本；不同版本体系不能猜测先后。
func CompareKernelVersions(a, b string) (int, error) {
	normalizeTime := func(value string) (string, bool) {
		layout := ""
		switch len(value) {
		case 8:
			layout = "20060102"
		case 12:
			layout = "200601021504"
		case 14:
			layout = "20060102150405"
		default:
			return "", false
		}
		parsed, err := time.Parse(layout, value)
		if err != nil {
			return "", false
		}
		return parsed.Format("20060102150405"), true
	}
	left, leftTime := normalizeTime(a)
	right, rightTime := normalizeTime(b)
	if leftTime && rightTime {
		return strings.Compare(left, right), nil
	}
	if leftTime != rightTime {
		return 0, fmt.Errorf("不能混用时间版本与语义版本")
	}
	if !strings.HasPrefix(a, "v") {
		a = "v" + a
	}
	if !strings.HasPrefix(b, "v") {
		b = "v" + b
	}
	if !semver.IsValid(a) || !semver.IsValid(b) {
		return 0, fmt.Errorf("内核版本必须为有效时间版本或语义版本")
	}
	return semver.Compare(a, b), nil
}
