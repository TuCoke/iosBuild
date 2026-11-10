// 脚本可见的符号表。
//
// yaegi 的 Use() 要的是"包路径 → 符号名 → reflect.Value"的映射，
// 这里只登记 script/api 一个包：脚本 import 它就能调全部原生能力（经 api.Call）。
//
// 为什么不做全 SDK 的自动导出：Go 的反射拿不到包级函数，只能手写；
// 而 222 个函数手写既易漏又易与实现漂移。走 api.Call 一个入口，
// 新增原生方法时这份表不用动。
package api

import (
	"reflect"

	"github.com/traefik/yaegi/interp"
)

// Symbols 供 interp.Use 使用。
var Symbols = interp.Exports{
	"autogov1/v2/sdk/ios/script/api/api": {
		"Call":        reflect.ValueOf(Call),
		"CallString":  reflect.ValueOf(CallString),
		"MustCall":    reflect.ValueOf(MustCall),
		"Log":         reflect.ValueOf(Log),
		"Param":       reflect.ValueOf(Param),
		"ParamString": reflect.ValueOf(ParamString),
		"ParamFloat":  reflect.ValueOf(ParamFloat),
		"SetResult":   reflect.ValueOf(SetResult),
		"Sleep":       reflect.ValueOf(Sleep),

	},
}

// 确认 Symbols 的键名与包路径一致（go vet 看不到这类错误，
// 一旦写错，运行时会报 "package not found"，所以在编译期用断言兜一层）。
var _ = func() bool {
	const pkgPath = "autogov1/v2/sdk/ios/script/api"
	if _, ok := Symbols[pkgPath+"/api"]; !ok {
		panic("script/api: Symbols 的包路径与 import 路径不一致")
	}
	return true
}()
