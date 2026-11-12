//go:build ios && cgo

#import <Foundation/Foundation.h>
#import <objc/message.h>
#include <stdlib.h>
#include <string.h>

// 只连接本项目 ObjC 宿主；SDK 独立 worker 没有该类时明确报告不可用。
char *agov_background_request(const char *command,const char *owner,int enabled) {
    @autoreleasepool {
        Class cls=NSClassFromString(@"AGBackgroundRuntime");
        SEL shared=NSSelectorFromString(@"shared");SEL apply=NSSelectorFromString(@"command:owner:enabled:");
        NSDictionary *result=nil;
        if(cls && [cls respondsToSelector:shared]){
            id manager=((id(*)(id,SEL))objc_msgSend)(cls,shared);
            if([manager respondsToSelector:apply])result=((id(*)(id,SEL,id,id,BOOL))objc_msgSend)(manager,apply,[NSString stringWithUTF8String:command ?: "status"],[NSString stringWithUTF8String:owner ?: ""],enabled!=0);
        }
        if(!result)result=@{@"available":@NO,@"reason":@"当前进程没有 ObjC 宿主后台管理器；子进程不等于已获得常驻能力"};
        NSData *data=[NSJSONSerialization dataWithJSONObject:result options:0 error:NULL];
        if(!data)return strdup("{\"available\":false,\"reason\":\"后台状态编码失败\"}");
        char *bytes=malloc(data.length+1);if(!bytes)return NULL;
        memcpy(bytes,data.bytes,data.length);bytes[data.length]=0;return bytes;
    }
}
