//go:build ios && cgo

// 原生能力层实现：76 个 Bridge* 与参考实现同名，内部复用已有的分发函数。
//
// 【为什么不重写一遍】
// 触摸/应用/设备/悬浮层这些能力，我们已经有一套实现（AGInput / AGApplication /
// AGDevice / AGOverlay / AGUtils）。这一层只是把它们按参考实现的命名暴露出去，
// 避免出现"两套实现、行为不一致"。
#import "runtime.h"
#import "ag_bridge.h"
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <dlfcn.h>

// 采集回调由 streambridge_ios.m 管理，统一编码器、停止和借用内存的生命周期。

// ---- 内部助手：调既有分发函数并解析 JSON ----

static id AGBridgeCallJSON(NSString *op, NSDictionary *args) {
    ag_ios_result r = {0};
    if ([op hasPrefix:@"device."])      r = AGDevice(op, args);
    else if ([op hasPrefix:@"app."])    r = AGApplication(op, args);
    else if ([op hasPrefix:@"motion."] || [op hasPrefix:@"ime."] || [op hasPrefix:@"input."]) r = AGInput(op, args);
    else if ([op hasPrefix:@"hud."] || [op hasPrefix:@"console."]) r = AGOverlay(op, args);
    else if ([op hasPrefix:@"utils."])  r = AGUtils(op, args);
    else if ([op hasPrefix:@"uiacc."])  r = AGUIAcc(op, args);
    else return nil;
    if (r.code != 0 || !r.data || r.length == 0) {
        if (r.data) ag_ios_free(r.data);
        return nil;
    }
    NSData *data = [NSData dataWithBytes:r.data length:r.length];
    ag_ios_free(r.data);
    id obj = [NSJSONSerialization JSONObjectWithData:data options:0 error:NULL];
    return obj;
}

static long long AGBridgeInt(NSString *op, NSDictionary *args, NSString *key) {
    id obj = AGBridgeCallJSON(op, args);
    if (![obj isKindOfClass:[NSDictionary class]]) return -1;
    id v = ((NSDictionary *)obj)[key];
    return v ? [v longLongValue] : -1;
}

static char *AGStrDup(NSString *s) {
    if (!s.length) return NULL;
    const char *utf8 = s.UTF8String;
    size_t len = strlen(utf8);
    char *out = malloc(len + 1);
    if (out) memcpy(out, utf8, len + 1);
    return out;
}

static char *AGBridgeStr(NSString *op, NSDictionary *args, NSString *key) {
    id obj = AGBridgeCallJSON(op, args);
    if (![obj isKindOfClass:[NSDictionary class]]) return NULL;
    id v = ((NSDictionary *)obj)[key];
    return [v isKindOfClass:[NSString class]] ? AGStrDup((NSString *)v) : NULL;
}

static NSString *AGString(const char *s) {
    return s ? [NSString stringWithUTF8String:s] : @"";
}

// ---- 输入 ----

static int AGBridgePointer(double x,double y,int finger,NSString *phase){
    return AGBridgeInt(@"motion.pointer",@{@"x":@(x),@"y":@(y),@"id":@(finger),@"phase":phase},@"submitted")>=0?0:-1;
}
int BridgeTouchDownFinger(double x,double y,int finger){return AGBridgePointer(x,y,finger,@"down");}
int BridgeTouchMoveFinger(double x,double y,int finger){return AGBridgePointer(x,y,finger,@"move");}
int BridgeTouchUpFinger(double x,double y,int finger){return AGBridgePointer(x,y,finger,@"up");}
int BridgePressHome(void) { return AGBridgeInt(@"device.button", @{@"button": @"home"}, @"submitted") >= 0 ? 0 : -1; }
int BridgePressKey(int page,int usage){return AGBridgeInt(@"ime.key",@{@"page":@(page),@"usage":@(usage)},@"submitted")>=0?0:-1;}

// ---- 屏幕 ----

void BridgeNativeSize(double *width,double *height){
    if(width)*width=0;if(height)*height=0;id display=AGBridgeCallJSON(@"device.display",@{});
    if(![display isKindOfClass:[NSDictionary class]])return;
    if(width)*width=[display[@"width"] doubleValue];if(height)*height=[display[@"height"] doubleValue];
}
double BridgeScreenScale(void) {
    id obj = AGBridgeCallJSON(@"device.display", @{});
    return [obj isKindOfClass:[NSDictionary class]] ? [((NSDictionary *)obj)[@"scale"] doubleValue] : 0;
}
int BridgeScreenOrientation(void) { return (int)AGBridgeInt(@"device.display", @{}, @"rotation"); }
int BridgeScreenBrightness(void) { return (int)AGBridgeInt(@"device.brightness_get", @{}, @"value"); }
void BridgeSetScreenBrightness(int percent) {
    double v = percent / 100.0;
    if (v < 0) v = 0;
    if (v > 1) v = 1;
    AGBridgeCallJSON(@"device.brightness", @{@"value": @(v)});
}

// ---- 应用 ----

int BridgeOpenApp(const char *bundleId) {
    return AGBridgeInt(@"app.launch", @{@"bundleId": AGString(bundleId)}, @"submitted") >= 0 ? 0 : -1;
}
int BridgeKillApp(const char *bundleId) {
    return AGBridgeInt(@"app.stop", @{@"bundleId": AGString(bundleId)}, @"stopped") >= 0 ? 0 : -1;
}
char *BridgeFrontmostApp(void) {
    id obj = AGBridgeCallJSON(@"app.foreground", @{});
    return [obj isKindOfClass:[NSDictionary class]] ? AGStrDup(((NSDictionary *)obj)[@"bundleId"]) : NULL;
}
char *BridgeSelfBundleID(void) {
    id obj = AGBridgeCallJSON(@"app.self", @{});
    return [obj isKindOfClass:[NSDictionary class]] ? AGStrDup(((NSDictionary *)obj)[@"bundleId"]) : NULL;
}
int BridgeOpenURL(const char *url) {
    return AGBridgeInt(@"app.open_url", @{@"url": AGString(url)}, @"opened") >= 0 ? 0 : -1;
}
char *BridgeGetAppList(void) {
    id obj = AGBridgeCallJSON(@"app.list_all", @{@"system": @NO});
    if (![obj isKindOfClass:[NSArray class]]) return NULL;
    NSData *json = [NSJSONSerialization dataWithJSONObject:obj options:0 error:NULL];
    return json ? AGStrDup([[NSString alloc] initWithData:json encoding:NSUTF8StringEncoding]) : NULL;
}
char *BridgeGetAppName(const char *bundleId) {
    return AGBridgeStr(@"app.info", @{@"bundleId": AGString(bundleId)}, @"name");
}
char *BridgeGetAppVersion(const char *bundleId) {
    return AGBridgeStr(@"app.info", @{@"bundleId": AGString(bundleId)}, @"version");
}
char *BridgeGetAppBundlePath(const char *bundleId) {
    return AGBridgeStr(@"app.bundle_path", @{@"bundleId": AGString(bundleId)}, @"path");
}
char *BridgeGetAppDataPath(const char *bundleId) {
    return AGBridgeStr(@"app.data_path", @{@"bundleId": AGString(bundleId)}, @"path");
}
int BridgeIsAppInstalled(const char *bundleId) {
    return (int)AGBridgeInt(@"app.installed", @{@"bundleId": AGString(bundleId)}, @"installed");
}
// 图标是 PNG 二进制（不是 JSON），所以不能走 AGBridgeCallJSON，直接拿 ag_ios_result。
int BridgeGetAppIcon(const char *bundleId, void **outData, size_t *outLen) {
    if (!bundleId || !outData || !outLen) return -1;
    ag_ios_result r = AGApplication(@"app.icon", @{@"bundleId": AGString(bundleId)});
    if (r.code != 0 || !r.data || r.length == 0) {
        if (r.data) ag_ios_free(r.data);
        return -1;
    }
    void *copy = malloc(r.length);
    if (!copy) { ag_ios_free(r.data); return -1; }
    memcpy(copy, r.data, r.length);
    ag_ios_free(r.data);
    *outData = copy;
    *outLen = r.length;
    return 0;
}
int BridgeInstallApp(const char *path) {
    return AGBridgeInt(@"app.install", @{@"path": AGString(path)}, @"installed") >= 0 ? 0 : -1;
}
int BridgeUninstallApp(const char *bundleId) {
    return AGBridgeInt(@"app.uninstall", @{@"bundleId": AGString(bundleId)}, @"uninstalled") >= 0 ? 0 : -1;
}
int BridgeClearAppData(const char *bundleId) {
    return AGBridgeInt(@"app.clear", @{@"bundleId": AGString(bundleId)}, @"cleared") >= 0 ? 0 : -1;
}

// ---- 系统信息 ----

char *BridgeDeviceModel(void) { return AGBridgeStr(@"device.info", @{}, @"model"); }
char *BridgeSystemVersion(void) { return AGBridgeStr(@"device.info", @{}, @"osVersion"); }
// 序列号走 libMobileGestalt 的 MGCopyAnswer（参考实现壳里也是这个符号）。
char *BridgeSerialNumber(void) {
    typedef CFStringRef (*MGCopyAnswerFn)(CFStringRef);
    MGCopyAnswerFn copyAnswer = (MGCopyAnswerFn)AGSymbol("MGCopyAnswer");
    if (!copyAnswer) return NULL;
    CFStringRef value = copyAnswer(CFSTR("SerialNumber"));
    if (!value) return NULL;
    char *out = AGStrDup((NSString *)value);
    CFRelease(value);
    return out;
}
int BridgeBatteryLevel(void) { return (int)AGBridgeInt(@"device.battery", @{}, @"level"); }
int BridgeBatteryState(void) { return (int)AGBridgeInt(@"device.battery_status", @{}, @"status"); }
int BridgeIsScreenOn(void) { return (int)AGBridgeInt(@"device.screen_on", @{}, @"on"); }
int BridgeIsScreenLocked(void) {
    long long unlocked = AGBridgeInt(@"device.unlocked", @{}, @"unlocked");
    return unlocked < 0 ? -1 : (unlocked ? 0 : 1);
}
long long BridgeTotalMemory(void) { return AGBridgeInt(@"device.memory", @{}, @"total"); }
long long BridgeAvailableMemory(void) { return AGBridgeInt(@"device.memory", @{}, @"avail"); }
int BridgeKeepScreenOn(int on) {
    return AGBridgeInt(@"device.keep_awake", @{@"on": @(on != 0)}, @"submitted") >= 0 ? 0 : -1;
}
int BridgeWakeUp(void) { return AGBridgeInt(@"device.wake", @{}, @"submitted") >= 0 ? 0 : -1; }
int BridgeGetPidByName(const char *name) { return (int)AGBridgeInt(@"system.pid", @{@"name": AGString(name)}, @"pid"); }
long long BridgeGetMemoryUsageKB(int pid) { return AGBridgeInt(@"system.memory", @{@"pid": @(pid)}, @"kb"); }
double BridgeGetCpuUsage(int pid) {
    id obj = AGBridgeCallJSON(@"system.cpu", @{@"pid": @(pid)});
    return [obj isKindOfClass:[NSDictionary class]] ? [((NSDictionary *)obj)[@"percent"] doubleValue] : 0;
}
int BridgeReboot(void) { return AGBridgeInt(@"device.reboot", @{}, @"submitted") >= 0 ? 0 : -1; }

// ---- 网络 ----

int BridgeVpnStatus(void) { return (int)AGBridgeInt(@"device.vpn_status", @{}, @"on"); }
int BridgeSetVPNEnabled(int enabled) {
    return AGBridgeInt(@"device.vpn_set", @{@"enabled": @(enabled != 0)}, @"ok") >= 0 ? 0 : -1;
}
int BridgeSetWiFiEnabled(int enabled) {
    return AGBridgeInt(@"device.wifi_set", @{@"enabled": @(enabled != 0)}, @"ok") >= 0 ? 0 : -1;
}

// ---- 文本 / 弹窗 ----

int BridgeShowToast(const char *text, double durationMs) {
    return AGBridgeInt(@"utils.toast", @{@"message": AGString(text), @"duration": @(durationMs)}, @"submitted") >= 0 ? 0 : -1;
}
int BridgeInputText(const char *text) {
    return AGBridgeInt(@"ime.text", @{@"text": AGString(text)}, @"submitted") >= 0 ? 0 : -1;
}
char *BridgeGetClipboard(void) { return AGBridgeStr(@"device.clipget", @{}, @"text"); }
int BridgeSetClipboard(const char *text) {
    return AGBridgeInt(@"device.clipset", @{@"text": AGString(text)}, @"submitted") >= 0 ? 0 : -1;
}
int BridgeAlert(const char *title, const char *content, const char *buttons) {
    NSMutableArray *list = [NSMutableArray array];
    if (buttons) {
        for (NSString *b in [AGString(buttons) componentsSeparatedByString:@","]) {
            if (b.length) [list addObject:b];
        }
    }
    return AGBridgeInt(@"utils.alert", @{@"title": AGString(title), @"content": AGString(content),
                                        @"buttons": list}, @"index") >= 0 ? 0 : -1;
}
int BridgeInputAlert(const char *title, const char *content, const char *placeholder, char **outText) {
    id obj = AGBridgeCallJSON(@"utils.input_alert", @{@"title": AGString(title), @"content": AGString(content),
                                                     @"placeholder": AGString(placeholder)});
    if (![obj isKindOfClass:[NSDictionary class]]) return -1;
    if (outText) *outText = AGStrDup(((NSDictionary *)obj)[@"text"]);
    return 0;
}

// ---- 悬浮层 ----

int BridgeHudCreate(int h) { (void)h; return 0; }
int BridgeHudDestroy(int h) { return AGBridgeInt(@"hud.destroy", @{@"id": @(h)}, @"ok") >= 0 ? 0 : -1; }
int BridgeHudShow(int h, int x1, int y1, int x2, int y2) {
    return AGBridgeInt(@"hud.show", @{@"id": @(h), @"x1": @(x1), @"y1": @(y1), @"x2": @(x2), @"y2": @(y2)}, @"ok") >= 0 ? 0 : -1;
}
int BridgeHudHide(int h) { return AGBridgeInt(@"hud.hide", @{@"id": @(h)}, @"ok") >= 0 ? 0 : -1; }
int BridgeHudIsVisible(int h) { return (int)AGBridgeInt(@"hud.visible", @{@"id": @(h)}, @"visible"); }
int BridgeHudSetPosition(int h, int x1, int y1, int x2, int y2) { return BridgeHudShow(h, x1, y1, x2, y2); }
int BridgeHudSetBgColor(int h, const char *hex) {
    return AGBridgeInt(@"hud.show", @{@"id": @(h), @"background": AGString(hex)}, @"ok") >= 0 ? 0 : -1;
}
int BridgeHudSetFontSize(int h, int size) {
    return AGBridgeInt(@"hud.show", @{@"id": @(h), @"textSize": @(size)}, @"ok") >= 0 ? 0 : -1;
}
int BridgeHudSetText(int h, const char *text) {
    return AGBridgeInt(@"hud.show", @{@"id": @(h), @"items": @[@{@"Text": AGString(text)}]}, @"ok") >= 0 ? 0 : -1;
}

int BridgeConsoleCreate(int h) { (void)h; return 0; }
int BridgeConsoleDestroy(int h) { return AGBridgeInt(@"console.destroy", @{@"id": @(h)}, @"ok") >= 0 ? 0 : -1; }
int BridgeConsoleShow(int h, int x, int y, int w, int hgt) {
    return AGBridgeInt(@"console.show", @{@"id": @(h), @"x": @(x), @"y": @(y), @"width": @(w), @"height": @(hgt)}, @"ok") >= 0 ? 0 : -1;
}
int BridgeConsoleHide(int h) { return AGBridgeInt(@"console.hide", @{@"id": @(h)}, @"ok") >= 0 ? 0 : -1; }
int BridgeConsoleIsVisible(int h) { return (int)AGBridgeInt(@"console.visible", @{@"id": @(h)}, @"visible"); }
int BridgeConsoleLog(int h, const char *text) {
    return AGBridgeInt(@"console.print", @{@"id": @(h), @"text": AGString(text)}, @"ok") >= 0 ? 0 : -1;
}
int BridgeConsoleClear(int h) { return AGBridgeInt(@"console.clear", @{@"id": @(h)}, @"ok") >= 0 ? 0 : -1; }
int BridgeConsoleSetPosition(int h, int x, int y, int w, int hgt) { return BridgeConsoleShow(h, x, y, w, hgt); }
// ConsoleSetSize：只改宽高、保持原位置。原生 console.show 是"位置+尺寸一起给"，
// 所以这里只传 width/height，x/y 交给原生沿用当前值。
int BridgeConsoleSetSize(int h, int w, int hgt) {
    return AGBridgeInt(@"console.show", @{@"id": @(h), @"width": @(w), @"height": @(hgt)}, @"ok") >= 0 ? 0 : -1;
}
int BridgeConsoleSetFontSize(int h, int size) {
    return AGBridgeInt(@"console.show", @{@"id": @(h), @"textSize": @(size)}, @"ok") >= 0 ? 0 : -1;
}
int BridgeConsoleSetBgColor(int h, const char *hex) {
    return AGBridgeInt(@"console.show", @{@"id": @(h), @"background": AGString(hex)}, @"ok") >= 0 ? 0 : -1;
}

// ---- 采集 ----

int BridgeH264SetCallback(void *cb){return AGH264CallbackSet((AGH264Callback)cb);}
int BridgeH264SetEncoderParams(int resolution,int bps,int fps){return AGH264ParamsSet(resolution,bps,fps);}
int BridgeH264StartEncoding(void){return AGH264Start();}
int BridgeH264StopEncoding(void){return AGH264Stop();}
int BridgeRawFrameSetCallback(void *cb){return AGRawCallbackSet((AGRawCallback)cb);}
int BridgeRawFrameStart(void){return AGRawStart();}
int BridgeRawFrameStop(void){return AGRawStop();}

char *agov_stream_error(void){return AGStreamFailureCopy();}

// ---- 进程 ----

int BridgeSpawnRootBinary(const char *path, const char *args, int *outPid) {
    NSMutableArray *list = [NSMutableArray array];
    if (args) {
        for (NSString *a in [AGString(args) componentsSeparatedByString:@" "]) {
            if (a.length) [list addObject:a];
        }
    }
    long long pid = AGBridgeInt(@"utils.spawn", @{@"path": AGString(path), @"args": list}, @"pid");
    if (pid <= 0) return -1;
    if (outPid) *outPid = (int)pid;
    return 0;
}
