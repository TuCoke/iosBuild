//go:build ios && cgo

// ioshost 是给 iOS 宿主 App 用的 C ABI 入口。
//
// SDK 本身是纯 Go 库（sdk/ios/...），直接被 Xcode 链接需要一个 c-archive：
//
//	GOOS=ios GOARCH=arm64 CGO_ENABLED=1 \
//	  go build -buildmode=c-archive -o host/ios/GoLib/libagovios.a ./cmd/ioshost
//
// 导出的两个函数（Swift/ObjC 侧用它们）：
//
//	char *agov_ios_invoke(const char *method, const char *payloadJson);
//	void  agov_ios_free(char *p);
//
// 约定：返回值由本库 malloc，调用方【必须】用 agov_ios_free 释放；返回 JSON：
//
//	{"ok":true,"data":...}  或  {"ok":false,"error":"..."}
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"
	"unsafe"

	"autogov1/v2/sdk/ios/app"
	"autogov1/v2/sdk/ios/capture"
	"autogov1/v2/sdk/ios/device"
	"autogov1/v2/sdk/ios/ime"
	"autogov1/v2/sdk/ios/motion"
)

type reply struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func encode(r reply) *C.char {
	b, err := json.Marshal(r)
	if err != nil {
		// 连错误都序列化不了时给一个固定的可解析结果，绝不返回非法 JSON。
		b = []byte(`{"ok":false,"error":"结果序列化失败"}`)
	}
	return C.CString(string(b))
}

func fail(err error) *C.char {
	if err == nil {
		return encode(reply{OK: true})
	}
	return encode(reply{OK: false, Error: err.Error()})
}

func okData(v any) *C.char { return encode(reply{OK: true, Data: v}) }

func input(payload *C.char) map[string]any {
	out := map[string]any{}
	if payload == nil {
		return out
	}
	s := C.GoString(payload)
	if s == "" {
		return out
	}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func num(m map[string]any, key string, def float64) float64 {
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return def
}

func str(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func boolean(m map[string]any, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

//export agov_ios_invoke
func agov_ios_invoke(method *C.char, payload *C.char) *C.char {
	if method == nil {
		return fail(contextError("缺少方法名"))
	}
	name := C.GoString(method)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := input(payload)

	switch name {
	case "capabilities":
		v, err := device.Capabilities(ctx)
		if err != nil {
			return fail(err)
		}
		return okData(v)

	case "device.info":
		v, err := device.Info(ctx)
		if err != nil {
			return fail(err)
		}
		return okData(v)

	case "device.display":
		v, err := device.Display(ctx)
		if err != nil {
			return fail(err)
		}
		return okData(v)

	case "motion.tap":
		if err := motion.Tap(ctx, num(args, "x", 0.5), num(args, "y", 0.5)); err != nil {
			return fail(err)
		}
		return okData(map[string]any{"injected": "tap"})

	case "motion.longpress":
		d := time.Duration(num(args, "durationMs", 600)) * time.Millisecond
		if err := motion.LongPress(ctx, num(args, "x", 0.5), num(args, "y", 0.5), d); err != nil {
			return fail(err)
		}
		return okData(map[string]any{"injected": "longpress", "durationMs": d.Milliseconds()})

	case "motion.swipe":
		d := time.Duration(num(args, "durationMs", 500)) * time.Millisecond
		if err := motion.Swipe(ctx, num(args, "x1", 0.5), num(args, "y1", 0.7), num(args, "x2", 0.5), num(args, "y2", 0.3), d); err != nil {
			return fail(err)
		}
		return okData(map[string]any{"injected": "swipe", "durationMs": d.Milliseconds()})

	case "capture.screenshot":
		// 截图走二进制，这里转 base64 交给宿主显示；iOS 侧是 JPEG/PNG 编码后的数据。
		frame, err := capture.Screenshot(ctx, capture.Options{
			Rotation: int(num(args, "rotation", 0)),
			MaxSize:  int(num(args, "maxSize", 1024)),
			JPEG:     boolean(args, "jpeg"),
			Quality:  num(args, "quality", 0.8),
		})
		if err != nil {
			return fail(err)
		}
		return okData(map[string]any{
			"mime":   frame.MIME,
			"width":  frame.Width,
			"height": frame.Height,
			"base64": base64.StdEncoding.EncodeToString(frame.Data),
		})

	case "app.foreground":
		v, err := app.Foreground(ctx)
		if err != nil {
			return fail(err)
		}
		return okData(v)

	case "app.launch":
		if err := app.Launch(ctx, str(args, "bundleId")); err != nil {
			return fail(err)
		}
		return okData(map[string]any{"launched": str(args, "bundleId")})

	case "ime.text":
		if err := ime.InputText(ctx, str(args, "text")); err != nil {
			return fail(err)
		}
		return okData(map[string]any{"typed": len(str(args, "text"))})

	default:
		return fail(contextError("未知方法: " + name))
	}
}

//export agov_ios_free
func agov_ios_free(p *C.char) {
	if p != nil {
		C.free(unsafe.Pointer(p))
	}
}

type stringError string

func (e stringError) Error() string { return string(e) }

func contextError(s string) error { return stringError(s) }

func main() {}
