//go:build ios && cgo

package main

/*
#include <stdlib.h>
char *agov_background_request(const char *command,const char *owner,int enabled);
*/
import "C"

import (
	"context"
	"encoding/json"
	"sync"
	"unsafe"

	"autogov1/v2/cmd/ioshost/debugapi"
	"autogov1/v2/sdk/ios/script/worker"
	"autogov1/v2/sdk/ios/video"
)

func backgroundRequest(command, owner string, enabled bool) map[string]any {
	cCommand, cOwner := C.CString(command), C.CString(owner)
	defer C.free(unsafe.Pointer(cCommand))
	defer C.free(unsafe.Pointer(cOwner))
	flag := C.int(0)
	if enabled {
		flag = 1
	}
	raw := C.agov_background_request(cCommand, cOwner, flag)
	if raw == nil {
		return map[string]any{"available": false, "reason": "后台管理器返回空响应"}
	}
	defer C.free(unsafe.Pointer(raw))
	var value map[string]any
	if err := json.Unmarshal([]byte(C.GoString(raw)), &value); err != nil {
		return map[string]any{"available": false, "reason": err.Error()}
	}
	return value
}

type backgroundVideo struct {
	*video.Stream
	once sync.Once
}

func (v *backgroundVideo) Close() error {
	err := v.Stream.Close()
	if err == nil {
		v.once.Do(func() { backgroundRequest("release", "debug-video", false) })
	}
	return err
}
func openBackgroundVideo(ctx context.Context, cfg video.Config) (debugapi.VideoSource, error) {
	added, _ := backgroundRequest("acquire", "debug-video", false)["added"].(bool)
	source, err := video.Start(ctx, cfg)
	if err != nil {
		if added {
			backgroundRequest("release", "debug-video", false)
		}
		return nil, err
	}
	return &backgroundVideo{Stream: source}, nil
}

// 同步服务需求与真实成功结果。稳定 owner 幂等，重复启动失败不能释放原有需求。
func backgroundBefore(name string) func(bool) {
	starts := map[string]string{"debugapi.start": "debug-api", "localapi.start": "local-api", "tunnel.start": "tunnel"}
	stops := map[string]string{"debugapi.stop": "debug-api", "localapi.stop": "local-api", "tunnel.stop": "tunnel"}
	if owner := starts[name]; owner != "" {
		added, _ := backgroundRequest("acquire", owner, false)["added"].(bool)
		return func(ok bool) {
			if !ok && added {
				backgroundRequest("release", owner, false)
			}
		}
	}
	if owner := stops[name]; owner != "" {
		return func(ok bool) {
			if ok {
				backgroundRequest("release", owner, false)
			}
		}
	}
	switch name {
	case "script.run", "script.remote", "runtime.diagnostics.worker":
		backgroundRequest("acquire", "script-worker", false)
		return func(bool) {
			if worker.Status()["running"] != true {
				backgroundRequest("release", "script-worker", false)
			}
		}
	}
	return nil
}
