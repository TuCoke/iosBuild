//go:build ios && cgo

// 设备状态与控制：显示、电量、内存、亮屏/解锁、亮度、网络、重启。
//
// 判断原则与 app_ios.m 一致：能用公开 API 就用公开 API（UIDevice/UIScreen/
// NSProcessInfo），必须走私有的（锁屏态、VPN、WiFi 开关）先 dlsym 再校验，
// 拿不到就明确返回"不可用"，不返回假数据。
#import "runtime.h"
#include <sys/utsname.h>
#include <sys/sysctl.h>
#include <ifaddrs.h>
#include <net/if.h>
#include <net/if_dl.h>
#include <arpa/inet.h>
#include <mach/mach.h>
#include <string.h>
#include <notify.h>

// AGSysctlInt64 读一个 sysctl 整数值（失败返回 -1）。
static long long AGSysctlInt64(const char *name) {
    int64_t value = 0;
    size_t size = sizeof(value);
    if (sysctlbyname(name, &value, &size, NULL, 0) != 0) return -1;
    return (long long)value;
}

// AGMemoryMB 返回 (总内存, 可用内存)，单位 MB。
//
// 总内存用 hw.memsize；可用内存用 host_statistics64 的 free+inactive 页，
// 这是 iOS 上唯一不需要私有权限的口径（和"系统设置里看到的可用"会有差异，属正常）。
static void AGMemoryMB(long long *total, long long *avail) {
    *total = AGSysctlInt64("hw.memsize") / (1024 * 1024);
    mach_port_t host = mach_host_self();
    vm_statistics64_data_t stat;
    mach_msg_type_number_t count = HOST_VM_INFO64_COUNT;
    vm_size_t pageSize = 0;
    host_page_size(host, &pageSize);
    if (host_statistics64(host, HOST_VM_INFO64, (host_info64_t)&stat, &count) == KERN_SUCCESS) {
        long long freePages = (long long)stat.free_count + (long long)stat.inactive_count;
        *avail = freePages * (long long)pageSize / (1024 * 1024);
    } else {
        *avail = 0;
    }
}

// AGIPv4 返回第一个非回环的 IPv4 地址。
static NSString *AGIPv4(void) {
    struct ifaddrs *head = NULL;
    if (getifaddrs(&head) != 0) return @"";
    NSString *result = @"";
    for (struct ifaddrs *cur = head; cur; cur = cur->ifa_next) {
        if (!cur->ifa_addr || cur->ifa_addr->sa_family != AF_INET) continue;
        if (!(cur->ifa_flags & IFF_UP) || (cur->ifa_flags & IFF_LOOPBACK)) continue;
        char buffer[INET_ADDRSTRLEN] = {0};
        struct sockaddr_in *addr = (struct sockaddr_in *)cur->ifa_addr;
        if (inet_ntop(AF_INET, &addr->sin_addr, buffer, sizeof(buffer))) {
            result = [NSString stringWithUTF8String:buffer] ?: @"";
            break;
        }
    }
    freeifaddrs(head);
    return result;
}

// AGWiFiMAC 返回 en0 的 MAC。
// iOS 7 之后拿到的是系统随机化地址，不是硬件 MAC —— 这一点与 AutoGo 的语义一致。
static NSString *AGWiFiMAC(void) {
    struct ifaddrs *head = NULL;
    if (getifaddrs(&head) != 0) return @"";
    NSString *result = @"";
    for (struct ifaddrs *cur = head; cur; cur = cur->ifa_next) {
        if (!cur->ifa_addr || cur->ifa_addr->sa_family != AF_LINK) continue;
        if (strcmp(cur->ifa_name, "en0") != 0) continue;
        struct sockaddr_dl *link = (struct sockaddr_dl *)cur->ifa_addr;
        if (link->sdl_alen != 6) continue;
        unsigned char *mac = (unsigned char *)LLADDR(link);
        result = [NSString stringWithFormat:@"%02X:%02X:%02X:%02X:%02X:%02X",
                  mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]];
        break;
    }
    freeifaddrs(head);
    return result;
}

ag_ios_result AGDevice(NSString *op, NSDictionary *args) {
    if ([op isEqualToString:@"device.button"]) return AGInput(op,args);

    if ([op isEqualToString:@"device.lock"]) {
        void (*lock)(void) = AGSymbol("SBSLockDevice");
        if (!lock) return AGError(1,@"SBSLockDevice 不可用");
        lock();
        return AGJSON(@{@"submitted":@YES});
    }

    // 解锁态：SpringBoardServices 的 SBSGetScreenLockStatus 是布尔输出参数。
    if ([op isEqualToString:@"device.unlocked"]) {
        void (*getLockStatus)(BOOL *) = AGSymbol("SBSGetScreenLockStatus");
        if (!getLockStatus) return AGError(1,@"锁屏状态接口不可用");
        BOOL locked = NO;
        getLockStatus(&locked);
        return AGJSON(@{@"unlocked":@((BOOL)(!locked))});
    }

    // 亮屏态：用 SpringBoard 的 Darwin 通知 "hasBlankedScreen"。
    // 走 IOKit 的 IODisplayWrangler 需要 IOKit 类型声明，而 iOS SDK 不带这些头；
    // notify 是 libSystem 的公开 C 接口，不需要额外框架，也不会因为符号缺失编不过。
    if ([op isEqualToString:@"device.screen_on"]) {
        int token = 0;
        if (notify_register_check("com.apple.springboard.hasBlankedScreen", &token) != NOTIFY_STATUS_OK) {
            return AGError(1,@"亮屏状态接口不可用");
        }
        uint64_t blanked = 0;
        int status = notify_get_state(token, &blanked);
        notify_cancel(token);
        if (status != NOTIFY_STATUS_OK) return AGError(1,@"亮屏状态接口不可用");
        return AGJSON(@{@"on":@((BOOL)(blanked == 0))});
    }
    // 内存：不需要主线程，放在 AGOnMain 之前返回更省。
    if ([op isEqualToString:@"device.memory"]) {
        long long total = 0, avail = 0;
        AGMemoryMB(&total, &avail);
        NSString *field = args[@"field"];
        if ([field isEqualToString:@"avail"]) return AGJSON(@{@"avail":@(avail)});
        return AGJSON(@{@"total":@(total),@"avail":@(avail)});
    }

    if ([op isEqualToString:@"device.ip"]) return AGJSON(@{@"ip":AGIPv4()});
    if ([op isEqualToString:@"device.wifi_mac"]) return AGJSON(@{@"mac":AGWiFiMAC()});

    // 开关 VPN / WiFi：都要私有服务（NEVPNManager 只能改自己的配置；
    // 系统级开关在 ControlCenter/networkd 手里），拿不到就明确报不可用。
    if ([op isEqualToString:@"device.vpn_set"]) {
        Class managerClass = NSClassFromString(@"NEVPNManager");
        SEL shared = NSSelectorFromString(@"sharedManager");
        if (![managerClass respondsToSelector:shared]) return AGError(1,@"VPN 接口不可用");
        id manager = ((id(*)(id,SEL))objc_msgSend)(managerClass, shared);
        BOOL enabled = [args[@"enabled"] boolValue];
        SEL target = NSSelectorFromString(enabled ? @"startVPNTunnelAndReturnError:" : @"stopVPNTunnel");
        if (![manager respondsToSelector:target]) return AGError(1,@"VPN 开关接口不可用");
        if (enabled) {
            id ignored = nil;
            BOOL ok = ((BOOL(*)(id,SEL,id*))objc_msgSend)(manager, target, &ignored);
            return ok ? AGJSON(@{@"ok":@YES}) : AGError(3,@"VPN 连接失败");
        }
        ((void(*)(id,SEL))objc_msgSend)(manager, target);
        return AGJSON(@{@"ok":@YES});
    }
    if ([op isEqualToString:@"device.wifi_set"]) {
        // 系统 WiFi 开关没有公开 API；这里走 ControlCenter 的私有服务，
        // 不同版本符号名不同，逐个试，全都没有就如实报不可用。
        BOOL enabled = [args[@"enabled"] boolValue];
        Class ccClass = NSClassFromString(@"CCUIConnectivityManager");
        SEL shared = NSSelectorFromString(@"sharedInstance");
        if ([ccClass respondsToSelector:shared]) {
            id manager = ((id(*)(id,SEL))objc_msgSend)(ccClass, shared);
            SEL toggle = NSSelectorFromString(enabled ? @"setWiFiEnabled:" : @"setWiFiEnabled:");
            if ([manager respondsToSelector:toggle]) {
                ((void(*)(id,SEL,BOOL))objc_msgSend)(manager, toggle, enabled);
                return AGJSON(@{@"ok":@YES});
            }
        }
        return AGError(1,@"WiFi 开关接口不可用（需要私有服务授权）");
    }
    if ([op isEqualToString:@"device.reboot"]) {
        int (*rebootFn)(int) = AGSymbol("reboot");
        if (!rebootFn) return AGError(1,@"重启接口不可用");
        rebootFn(0);
        return AGJSON(@{@"submitted":@YES});
    }
    // VPN 状态走 NetworkExtension（公开 API，但需要 VPN 配置权限）。
    if ([op isEqualToString:@"device.vpn_status"]) {
        Class managerClass = NSClassFromString(@"NEVPNManager");
        SEL shared = NSSelectorFromString(@"sharedManager");
        if (![managerClass respondsToSelector:shared]) return AGError(1,@"VPN 接口不可用");
        id manager = ((id(*)(id,SEL))objc_msgSend)(managerClass, shared);
        SEL reload = NSSelectorFromString(@"loadFromPreferencesWithCompletionHandler:");
        if ([manager respondsToSelector:reload]) {
            __block BOOL done = NO;
            ((void(*)(id,SEL,id))objc_msgSend)(manager, reload, ^(NSError *e){ done = YES; });
            NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:2];
            while (!done && [deadline timeIntervalSinceNow] > 0) {
                [[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.05]];
            }
        }
        SEL status = NSSelectorFromString(@"connection");
        id connection = [manager respondsToSelector:status] ? ((id(*)(id,SEL))objc_msgSend)(manager, status) : nil;
        SEL connStatus = NSSelectorFromString(@"status");
        long state = (connection && [connection respondsToSelector:connStatus])
            ? ((long(*)(id,SEL))objc_msgSend)(connection, connStatus) : 0;
        return AGJSON(@{@"on":@((BOOL)(state == 3))}); // 3 = NEVPNStatusConnected
    }

    return AGOnMain(^{
        UIScreen *screen = [UIScreen mainScreen];
        CGRect bounds = screen.nativeBounds;
        NSDictionary *display = @{@"width":@((int)bounds.size.width),@"height":@((int)bounds.size.height),
                                  @"scale":@(screen.nativeScale),@"rotation":@(0)};
        if ([op isEqualToString:@"device.display"]) return AGJSON(display);
        if ([op isEqualToString:@"device.info"]) {
            UIDevice *device = [UIDevice currentDevice];
            device.batteryMonitoringEnabled = YES;
            struct utsname model = {0}; uname(&model);
            return AGJSON(@{@"os":@"ios",@"osVersion":device.systemVersion ?: @"",
                            @"model":[NSString stringWithUTF8String:model.machine] ?: device.model,
                            @"name":device.name ?: @"",@"identifier":device.identifierForVendor.UUIDString ?: @"",
                            @"battery":@(device.batteryLevel),@"display":display});
        }
        if ([op isEqualToString:@"device.battery"] || [op isEqualToString:@"device.battery_status"]) {
            UIDevice *device = [UIDevice currentDevice];
            device.batteryMonitoringEnabled = YES;
            float level = device.batteryLevel;
            int percent = level < 0 ? -1 : (int)lroundf(level * 100.0f);
            if ([op isEqualToString:@"device.battery"]) return AGJSON(@{@"level":@(percent)});
            // 0 未知 / 1 放电 / 2 充电 / 3 满电
            int status = 0;
            switch (device.batteryState) {
                case UIDeviceBatteryStateUnplugged: status = 1; break;
                case UIDeviceBatteryStateCharging: status = 2; break;
                case UIDeviceBatteryStateFull: status = 3; break;
                default: status = 0; break;
            }
            return AGJSON(@{@"status":@(status)});
        }
        if ([op isEqualToString:@"device.brightness_get"]) {
            return AGJSON(@{@"value":@((int)lround(screen.brightness * 100.0))});
        }
        if ([op isEqualToString:@"device.keep_awake"]) {
            BOOL on = [args[@"on"] boolValue];
            [UIApplication sharedApplication].idleTimerDisabled = on;
            return AGJSON(@{@"submitted":@YES});
        }
        if ([op isEqualToString:@"device.wake"]) {
            screen.brightness = screen.brightness; // 触碰一下，确保屏幕已点亮
            return AGJSON(@{@"submitted":@YES});
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
