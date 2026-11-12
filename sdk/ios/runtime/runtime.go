// Package runtime 提供 iOS SDK 的结构化原生能力入口，不启动脚本解释器。
package runtime

import (
	"autogov1/v2/sdk/ios/internal/native"
	"autogov1/v2/sdk/ios/types"
	"context"
	"encoding/json"
	"fmt"
)

// JSON 方法白名单不包含进程启动、安装卸载或二进制缓冲；这些能力有独立接口。
var jsonMethods = map[string]bool{
	"device.screen_on": true, "device.unlocked": true, "device.wake": true, "device.lock": true, "device.brightness": true, "device.clipget": true, "device.clipset": true,
	"motion.key": true, "motion.pointer": true, "motion.frame": true,
	"app.info": true, "app.list_all": true, "app.stop": true, "app.open_url": true,
	"uiacc.snapshot": true, "system.cpu": true, "runtime.capabilities": true,
}

func CallJSON(ctx context.Context, method string, args any) (json.RawMessage, error) {
	if !jsonMethods[method] {
		return nil, fmt.Errorf("%w: 非结构化能力入口 %s", types.ErrUnsupported, method)
	}
	var out json.RawMessage
	if err := native.Call(ctx, method, args, &out); err != nil {
		return nil, err
	}
	if len(out) > 32<<20 || !json.Valid(out) {
		return nil, fmt.Errorf("%w: iOS 原生 JSON 结果无效", types.ErrUnavailable)
	}
	return out, nil
}
