//go:build ios && cgo

// 自检：不靠"符号在不在"猜能力，而是真的做一次，看系统认不认。
//
// 为什么需要它：capabilities 只查 dlsym 能不能解析到符号，
// 而符号存在 ≠ 能用（真机上没有 platform-application/frontboard 授权时，
// 符号在、调用也"成功返回"，但事件被系统丢掉、采集拿到黑帧）。
// 自检把"提交动作"和"可观测结果"分开报告，让人一眼看出卡在哪一步。
#import "runtime.h"
#include <unistd.h>

// AGSelfTestTouch 真投一次点击（按下 → 60ms → 抬起），报告事件是否被提交。
//
// 注意：能证明的是"我们成功把事件交给了 HID 系统"，不能证明
// "界面确实响应了"——后者只能在真机上看 UI 变化。
// 所以 detail 里写清楚，避免把"提交成功"当成"注入生效"。
static ag_ios_result AGSelfTestTouch(NSDictionary *args) {
    if (!AGInputAvailable(@"touch")) {
        return AGJSON(@{@"ok":@NO, @"detail":@"触摸注入符号不可用（IOHIDEventCreateDigitizer* 缺失）"});
    }
    double x = [args[@"x"] doubleValue];
    double y = [args[@"y"] doubleValue];
    if (x <= 0 || x >= 1) x = 0.5;
    if (y <= 0 || y >= 1) y = 0.5;

    ag_ios_result down = AGInput(@"motion.frame", @{@"contacts": @[@{@"id": @0, @"x": @(x), @"y": @(y)}]});
    BOOL downOK = (down.code == 0);
    NSString *downErr = down.code ? [[[NSString alloc] initWithBytes:down.data length:down.length encoding:NSUTF8StringEncoding] autorelease] : @"";
    ag_ios_free(down.data);

    usleep(60 * 1000);

    ag_ios_result up = AGInput(@"motion.frame", @{@"contacts": @[]});
    BOOL upOK = (up.code == 0);
    NSString *upErr = up.code ? [[[NSString alloc] initWithBytes:up.data length:up.length encoding:NSUTF8StringEncoding] autorelease] : @"";
    ag_ios_free(up.data);

    if (!downOK || !upOK) {
        return AGJSON(@{@"ok":@NO,
                        @"detail":[NSString stringWithFormat:@"按下失败: %@ / 抬起失败: %@",
                                   downErr.length ? downErr : @"(无)", upErr.length ? upErr : @"(无)"]});
    }
    return AGJSON(@{@"ok":@YES,
                    @"detail":@"事件已提交给 HID；是否被系统接受要看界面有无变化"});
}

// AGSelfTestCapture 真抓一帧，报告字节数与尺寸。
//
// 【为什么不再自己走 AGCopyScreenImage(0,0)】
// 那条路要求原生给出屏幕尺寸，模拟器给不出来 → 自检报"尺寸无效"，
// 而同一个能力走 capture.screenshot（带 maxSize 的 fallback）却是成功的。
// 现在统一走 AGCapture：能力判定与真实业务路径一致，才不会自相矛盾。
static ag_ios_result AGSelfTestCapture(NSDictionary *args) {
    ag_ios_result raw = AGCapture(@{@"maxSize": @512, @"jpeg": @YES, @"quality": @0.6});
    if (raw.code != 0) {
        NSString *reason = raw.data ? [[[NSString alloc] initWithBytes:raw.data length:raw.length encoding:NSUTF8StringEncoding] autorelease] : @"采集失败";
        if (raw.data) ag_ios_free(raw.data);
        return AGJSON(@{@"ok":@NO, @"detail": reason.length ? reason : @"采集失败"});
    }
    int bytes = (int)raw.length;
    int width = (int)raw.width;
    int height = (int)raw.height;
    if (raw.data) ag_ios_free(raw.data);
    if (bytes <= 0) {
        return AGJSON(@{@"ok":@NO, @"detail":@"采集返回空数据"});
    }
    return AGJSON(@{@"ok":@YES,
                    @"detail":[NSString stringWithFormat:@"抓到 %d 字节（%dx%d）", bytes, width, height],
                    @"width":@(width), @"height":@(height)});
}
ag_ios_result AGSelfTest(NSString *op, NSDictionary *args) {
    if (![op isEqualToString:@"runtime.selftest"]) return AGError(1,@"未知自检操作");
    ag_ios_result touch = AGSelfTestTouch(args);
    ag_ios_result capture = AGSelfTestCapture(args);

    NSData *touchData = touch.data ? [NSData dataWithBytes:touch.data length:touch.length] : [NSData data];
    NSData *captureData = capture.data ? [NSData dataWithBytes:capture.data length:capture.length] : [NSData data];
    ag_ios_free(touch.data);
    ag_ios_free(capture.data);

    NSMutableDictionary *result = [NSMutableDictionary dictionary];
    id touchJSON = touchData.length ? [NSJSONSerialization JSONObjectWithData:touchData options:0 error:NULL] : @{};
    id captureJSON = captureData.length ? [NSJSONSerialization JSONObjectWithData:captureData options:0 error:NULL] : @{};
    if ([touchJSON isKindOfClass:[NSDictionary class]]) result[@"touch"] = touchJSON;
    if ([captureJSON isKindOfClass:[NSDictionary class]]) result[@"capture"] = captureJSON;
    result[@"injected"] = @((BOOL)([[touchJSON objectForKey:@"ok"] boolValue] && [[captureJSON objectForKey:@"ok"] boolValue]));
    return AGJSON(result);
}
