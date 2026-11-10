//go:build ios && cgo

#import "runtime.h"
#include <stdlib.h>
#include <string.h>

ag_ios_result AGError(int code, NSString *message) {
    ag_ios_result r = AGData([(message ?: @"原生调用失败") dataUsingEncoding:NSUTF8StringEncoding]);
    r.code = code;
    return r;
}
ag_ios_result AGData(NSData *data) {
    ag_ios_result r = {0};
    if (data.length > 64 * 1024 * 1024) { r.code = 3; return r; }
    r.length = data.length;
    if (r.length) {
        r.data = malloc(r.length);
        if (!r.data) { r.code = 3; r.length = 0; return r; }
        memcpy(r.data, data.bytes, r.length);
    }
    return r;
}
ag_ios_result AGJSON(id object) {
    NSError *error = nil;
    NSData *data = [NSJSONSerialization dataWithJSONObject:object ?: @{} options:0 error:&error];
    if (!data) return AGError(3, error.localizedDescription);
    return AGData(data);
}
void ag_ios_free(void *data) { free(data); }

void AGLoadFrameworks(void) {
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        // 只加载系统框架，不加载参考包中的闭源库，不进行注入或授权修改。
        dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", RTLD_LAZY | RTLD_GLOBAL);
        dlopen("/System/Library/Frameworks/QuartzCore.framework/QuartzCore", RTLD_LAZY | RTLD_GLOBAL);
        dlopen("/System/Library/PrivateFrameworks/SpringBoardServices.framework/SpringBoardServices", RTLD_LAZY | RTLD_GLOBAL);
        dlopen("/System/Library/PrivateFrameworks/MobileCoreServices.framework/MobileCoreServices", RTLD_LAZY | RTLD_GLOBAL);
    });
}
void *AGSymbol(const char *name) {
    AGLoadFrameworks();
    return dlsym(RTLD_DEFAULT, name);
}

// 超时任务不会保留 Go 指针。排队阶段超时直接取消；已经开始的写操作返回结果未知。
@interface AGMainJob : NSObject {
@public NSCondition *condition;
    ag_ios_result (^work)(void);
    ag_ios_result result;
    BOOL started, finished, abandoned;
}
@end
@implementation AGMainJob
- (id)init { if ((self = [super init])) condition = [NSCondition new]; return self; }
- (void)dealloc { free(result.data); [work release]; [condition release]; [super dealloc]; }
@end

ag_ios_result AGOnMain(ag_ios_result (^work)(void)) {
    if ([NSThread isMainThread]) return work();
    AGMainJob *job = [AGMainJob new];
    job->work = [work copy];
    dispatch_async(dispatch_get_main_queue(), ^{
        @autoreleasepool {
            [job->condition lock];
            if (job->abandoned) { [job->condition unlock]; return; }
            job->started = YES;
            [job->condition unlock];
            ag_ios_result r;
            @try { r = job->work(); }
            @catch (NSException *ex) { r = AGError(4, ex.reason); }
            [job->condition lock];
            job->result = r;
            job->finished = YES;
            [job->condition broadcast];
            [job->condition unlock];
        }
    });
    NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:3];
    [job->condition lock];
    while (!job->finished && [job->condition waitUntilDate:deadline]) {}
    ag_ios_result r;
    if (job->finished) { r = job->result; job->result.data = NULL; }
    else { job->abandoned = YES; r = AGError(job->started ? 4 : 6, @"UIKit 主线程未及时返回；宿主必须运行主线程事件循环"); }
    [job->condition unlock];
    [job release];
    return r;
}

ag_ios_result ag_ios_call(const char *method, const uint8_t *json, size_t length) {
    @autoreleasepool {
        if (!method || length > 1024 * 1024) return AGError(2, @"请求参数过大或方法缺失");
        NSString *op = [NSString stringWithUTF8String:method];
        NSData *data = [NSData dataWithBytes:json length:length];
        NSError *error = nil;
        id value = [NSJSONSerialization JSONObjectWithData:data options:NSJSONReadingFragmentsAllowed error:&error];
        if (error || (value != [NSNull null] && ![value isKindOfClass:[NSDictionary class]])) return AGError(2, @"原生参数必须是对象");
        NSDictionary *args = [value isKindOfClass:[NSDictionary class]] ? value : @{};
        AGLoadFrameworks();
        @try {
            if ([op isEqualToString:@"runtime.capabilities"]) {
                return AGOnMain(^{
                    id workspaceClass = NSClassFromString(@"LSApplicationWorkspace");
                    id workspace = [workspaceClass respondsToSelector:NSSelectorFromString(@"defaultWorkspace")] ? ((id(*)(id,SEL))objc_msgSend)(workspaceClass, NSSelectorFromString(@"defaultWorkspace")) : nil;
                    BOOL capture = AGSymbol("CARenderServerRenderDisplay") != NULL;
                    return AGJSON(@{@"touch":@(AGInputAvailable(@"touch")),@"keyboard":@(AGInputAvailable(@"keyboard")),@"unicode":@(AGInputAvailable(@"unicode")),@"capture":@(capture),@"video":@(capture),@"launchApp":@([workspace respondsToSelector:NSSelectorFromString(@"openApplicationWithBundleID:")]),@"foregroundApp":@((BOOL)(AGSymbol("SBSCopyFrontmostApplicationDisplayIdentifier") != NULL)),@"lockScreen":@((BOOL)(AGSymbol("SBSLockDevice") != NULL))});
                });
            }
            if ([op hasPrefix:@"device."]) return AGDevice(op,args);
            if ([op hasPrefix:@"app."]) return AGApplication(op,args);
            if ([op hasPrefix:@"motion."] || [op hasPrefix:@"ime."]) return AGInput(op,args);
            if ([op isEqualToString:@"capture.image"]) return AGCapture(args);
            // capture.json：把同一帧包成 base64 JSON，给"只能吃 JSON 的调用方"（脚本 api.Call）用。
            // capture.image 返回二进制，api.Call 解析 JSON 会报 invalid character —— 踩过。
            if ([op isEqualToString:@"capture.json"]) {
                ag_ios_result raw = AGCapture(args);
                if (raw.code != 0) return raw;
                NSData *data = raw.data ? [NSData dataWithBytes:raw.data length:raw.length] : [NSData data];
                if (raw.data) ag_ios_free(raw.data);
                return AGJSON(@{@"base64": [data base64EncodedStringWithOptions:0],
                                @"bytes": @(data.length),
                                @"width": @((int)raw.width),
                                @"height": @((int)raw.height)});
            }
            if ([op isEqualToString:@"capture.release"]) { AGReleaseCaptureSurface(); return AGJSON(@{}); }
            if ([op hasPrefix:@"video."]) return AGVideo(op,args);
            if ([op hasPrefix:@"system."]) return AGSystem(op,args);
            if ([op hasPrefix:@"utils."]) return AGUtils(op,args);
            if ([op hasPrefix:@"uiacc."]) return AGUIAcc(op,args);
            if ([op hasPrefix:@"hud."]) return AGOverlay(op,args);
            if ([op hasPrefix:@"console."]) return AGOverlay(op,args);
            if ([op hasPrefix:@"apkctl."]) return AGAPKCtl(op,args);
            if ([op isEqualToString:@"runtime.selftest"]) return AGSelfTest(op,args);
            return AGError(1,[NSString stringWithFormat:@"尚未提供此原生能力: %@", op]);
        } @catch (NSException *ex) {
            return AGError(4,[NSString stringWithFormat:@"原生异常，需核对是否部分执行: %@",ex.reason]);
        }
    }
}
