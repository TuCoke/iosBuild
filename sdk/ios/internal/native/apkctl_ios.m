//go:build ios && cgo

#import "runtime.h"

// apkctl 的真实入口在 Go TCP 客户端和宿主 JavaScriptCore 服务。
// 保留原生分派器明确报错，防止旧的轮询接口继续返回伪成功。
ag_ios_result AGAPKCtl(NSString *op, NSDictionary *args) {
    return AGError(1,@"外壳控制请调用 apkctl.Eval/SetCallback/RegEvent；此原生轮询接口不再提供");
}
