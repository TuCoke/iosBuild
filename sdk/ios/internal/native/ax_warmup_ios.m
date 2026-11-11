//go:build ios && cgo

#import "runtime.h"
#include <unistd.h>
#include <stdlib.h>

// 环境恢复与缓存标记由 ax_runtime_ios.m 管理，预热只负责主线程查询和有界等待。
ag_ios_result AGAXWarmup(NSDictionary *args) {
    int timeout=[args[@"timeoutMs"] intValue];if(timeout<50 || timeout>2500)return AGError(2,@"AX 预热超时范围为 50..2500ms");
    ag_ios_result prepared=AGAXEnvironment(NO);if(prepared.code!=0)return prepared;free(prepared.data);
    BOOL found=NO;int attempts=0;NSDate *deadline=[NSDate dateWithTimeIntervalSinceNow:timeout/1000.0];ag_ios_result failure={0};
    do {
        ag_ios_result probe=AGOnMain(^{
            Class cls=NSClassFromString(@"AXUIElement");SEL selector=NSSelectorFromString(@"uiApplicationAtCoordinate:");
            if(![cls respondsToSelector:selector])return AGError(1,@"AX 中心查询入口不可用");
            CGSize size=UIScreen.mainScreen.bounds.size;
            id app=((id(*)(id,SEL,CGPoint))objc_msgSend)(cls,selector,CGPointMake(size.width/2,size.height/2));
            return AGJSON(@{@"found":@(app!=nil)});
        });
        attempts++;
        if(probe.code!=0){failure=probe;break;}
        NSData *data=[NSData dataWithBytes:probe.data length:probe.length];free(probe.data);
        NSDictionary *reply=[NSJSONSerialization JSONObjectWithData:data options:0 error:nil];found=[reply[@"found"] boolValue];
        if(found || [deadline timeIntervalSinceNow]<=0)break;
        usleep(50000);
    } while([deadline timeIntervalSinceNow]>0);
    BOOL restore=[args[@"restore"] boolValue];
    if(restore) {
        ag_ios_result restored=AGAXRestoreAfterWarmup();
        if(restored.code!=0){free(failure.data);return restored;}free(restored.data);
    }
    if(failure.code!=0)return failure;
    return AGJSON(@{@"applicationFound":@(found),@"attempts":@(attempts),@"restored":@(restore),@"mobileIdentity":@(geteuid()==501)});
}
