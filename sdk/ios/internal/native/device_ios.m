//go:build ios && cgo

#import "runtime.h"
#include <sys/utsname.h>

ag_ios_result AGDevice(NSString *op, NSDictionary *args) {
    if ([op isEqualToString:@"device.button"]) return AGInput(op,args);
    if ([op isEqualToString:@"device.lock"]) {
        void (*lock)(void) = AGSymbol("SBSLockDevice");
        if (!lock) return AGError(1,@"SBSLockDevice 不可用");
        lock();
        return AGJSON(@{@"submitted":@YES});
    }
    return AGOnMain(^{
        UIScreen *screen = [UIScreen mainScreen];
        CGRect bounds = screen.nativeBounds;
        NSDictionary *display = @{@"width":@((int)bounds.size.width),@"height":@((int)bounds.size.height),@"scale":@(screen.nativeScale)};
        if ([op isEqualToString:@"device.display"]) return AGJSON(display);
        if ([op isEqualToString:@"device.info"]) {
            UIDevice *device = [UIDevice currentDevice];
            device.batteryMonitoringEnabled = YES;
            struct utsname model = {0}; uname(&model);
            return AGJSON(@{@"os":@"ios",@"osVersion":device.systemVersion ?: @"",@"model":[NSString stringWithUTF8String:model.machine] ?: device.model,@"name":device.name ?: @"",@"identifier":device.identifierForVendor.UUIDString ?: @"",@"battery":@(device.batteryLevel),@"display":display});
        }
        if ([op isEqualToString:@"device.clipget"]) return AGJSON(@{@"text":[UIPasteboard generalPasteboard].string ?: @""});
        if ([op isEqualToString:@"device.clipset"]) {
            [UIPasteboard generalPasteboard].string = args[@"text"] ?: @"";
            return AGJSON(@{@"submitted":@YES});
        }
        if ([op isEqualToString:@"device.brightness"]) {
            double value = [args[@"value"] doubleValue];
            if (!isfinite(value) || value < 0 || value > 1) return AGError(2,@"亮度越界");
            screen.brightness = value;
            return AGJSON(@{@"submitted":@YES});
        }
        return AGError(1,@"未知设备操作");
    });
}
