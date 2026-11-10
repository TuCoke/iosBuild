// Package utils 对齐 AutoGo iOS 的工具函数（12 个）。
//
// 分两类：
//   · 纯 Go：Random / Sleep / 类型互转（I2s、S2i、F2s、S2f、B2s、S2b）
//   · 走原生：Toast / Alert / InputAlert（要 UIKit 窗口，Go 侧起不来）、
//             ExecBinary（要 posix_spawn，且必须有 platform-application 权限）
package utils

import (
	"math/rand"
	"strconv"
	"time"

	"autogov1/v2/sdk/ios/internal/bridge"
)

// randomUint64 用标准库全局随机源（Go 1.20+ 自带随机种子，不需要手工 Seed）。
// 这里只用于拟人化的抖动/间隔，不承担密码学用途。
func randomUint64() uint64 { return rand.Uint64() }

// Toast 在屏幕上弹一个短提示。x/y 为像素坐标，duration 为毫秒。
func Toast(message string, x, y, duration int) {
	_ = bridge.Do("utils.toast", map[string]any{"message": message, "x": x, "y": y, "duration": duration}, nil)
}

// Alert 弹一个确认框，返回被点击按钮的下标（0 或 1）。弹不出来时返回 -1，
// 让调用方能把"没人点"与"点了第 0 个"区分开。
func Alert(title, content, btn1Text, btn2Text string) int {
	var out struct {
		Index int `json:"index"`
	}
	if !bridge.DoTimeout("utils.alert", map[string]any{
		"title": title, "content": content, "buttons": []string{btn1Text, btn2Text},
	}, &out, 5*time.Minute) {
		return -1
	}
	return out.Index
}

// InputAlert 弹一个带输入框的对话框；第二个返回值表示用户是否点了确认。
func InputAlert(title, content, placeholder, defaultText, btn1Text, btn2Text string) (string, bool) {
	var out struct {
		Text    string `json:"text"`
		Confirm bool   `json:"confirm"`
	}
	if !bridge.DoTimeout("utils.input_alert", map[string]any{
		"title": title, "content": content, "placeholder": placeholder,
		"default": defaultText, "buttons": []string{btn1Text, btn2Text},
	}, &out, 5*time.Minute) {
		return "", false
	}
	return out.Text, out.Confirm
}

// ExecBinary 执行一个可执行文件，返回退出码；失败返回 -1。
// 注意：iOS 上必须有 platform-application（TrollStore/越狱）才允许这样起进程。
func ExecBinary(path string, args ...string) int {
	var out struct {
		Code int `json:"code"`
	}
	if !bridge.DoTimeout("utils.exec", map[string]any{"path": path, "args": args}, &out, 2*time.Minute) {
		return -1
	}
	return out.Code
}

// Random 返回 [min, max] 闭区间内的随机整数（含两端，与 AutoGo 一致）。
func Random(min, max int) int {
	if max < min {
		min, max = max, min
	}
	n := max - min + 1
	if n <= 0 {
		return min
	}
	return min + int(randomUint64()%uint64(n))
}

// Sleep 休眠指定毫秒。
func Sleep(i int) {
	if i <= 0 {
		return
	}
	time.Sleep(time.Duration(i) * time.Millisecond)
}

func I2s(i int) string { return strconv.Itoa(i) }

func S2i(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func F2s(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func S2f(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func B2s(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func S2b(s string) bool {
	b, _ := strconv.ParseBool(s)
	return b
}
