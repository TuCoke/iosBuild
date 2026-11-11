//go:build ios && cgo

#import "runtime.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// C ABI 只包装共享采集器；返回值由调用方 free，错误缓冲由调用方拥有。
char *AXSweepNodesJSON(char *buf, size_t bufLen) {
    @autoreleasepool {
        NSString *failure=nil;
        NSArray *nodes=AGAXCollectNodes(3000,&failure);
        NSData *json=nodes ? [NSJSONSerialization dataWithJSONObject:nodes options:0 error:NULL] : nil;
        if(!json){if(buf && bufLen)snprintf(buf,bufLen,"%s",(failure ?: @"AX JSON 编码失败").UTF8String);return NULL;}
        char *out=malloc(json.length+1);
        if(!out){if(buf && bufLen)snprintf(buf,bufLen,"%s","AX JSON 内存分配失败");return NULL;}
        memcpy(out,json.bytes,json.length);out[json.length]=0;return out;
    }
}
