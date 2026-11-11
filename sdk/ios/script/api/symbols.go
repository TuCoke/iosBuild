package api

import (
	"reflect"

	"github.com/traefik/yaegi/interp"
)

// Symbols 绑定本次运行的方法值，不能用包级任务状态替代。
func (s *Session) Symbols() interp.Exports {
	return interp.Exports{"autogov1/v2/sdk/ios/script/api/api": {
		"Args": reflect.ValueOf((*Args)(nil)), "Call": reflect.ValueOf(s.Call), "CallString": reflect.ValueOf(s.CallString), "MustCall": reflect.ValueOf(s.MustCall), "Log": reflect.ValueOf(s.Log), "Param": reflect.ValueOf(s.Param), "ParamString": reflect.ValueOf(s.ParamString), "ParamFloat": reflect.ValueOf(s.ParamFloat), "SetResult": reflect.ValueOf(s.SetResult), "Sleep": reflect.ValueOf(s.Sleep),
	}}
}
func SDKPackageCount() int { return len(SDKPackages) }
