//go:build ios && cgo

#import "runtime.h"

// Go API 与 C ABI 复用采集与字段归一化，禁止维护两套不同的控件树。
ag_ios_result AGUIAcc(NSString *op, NSDictionary *args) {
    if([op isEqualToString:@"uiacc.prepare"])return AGAXEnvironment(NO);
    if([op isEqualToString:@"uiacc.restore"])return AGAXEnvironment(YES);
    if(![op isEqualToString:@"uiacc.snapshot"])return AGError(1,@"未知控件操作");
    NSUInteger limit=[args[@"limit"] unsignedIntegerValue];
    if(limit==0 || limit>8000)limit=3000;
    NSString *failure=nil;
    NSArray *nodes=AGAXCollectNodes(limit,&failure);
    if(!nodes)return AGError(3,failure ?: @"AX 采集失败");
    return AGJSON(@{@"nodes":nodes});
}
