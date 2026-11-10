// AGBridge.m
#import "AGBridge.h"
#import "AGSDKLoader.h"

@interface AGBridge ()
/// JSON 转义。放在类扩展里，避免"实现在后、调用在前"导致 clang 报找不到方法。
+ (NSString *)escape:(NSString *)s;
@end

@implementation AGBridge

+ (NSString *)invoke:(NSString *)method payload:(NSString *)payload {
    NSString *result = [[AGSDKLoader shared] invoke:method payload:payload];
    if (result == nil) {
        NSString *reason = [AGSDKLoader shared].lastError ?: @"未知错误";
        return [NSString stringWithFormat:@"{\"ok\":false,\"error\":\"%@\"}", [self escape:reason]];
    }
    return result;
}

+ (NSString *)statusLine {
    AGSDKLoader *loader = [AGSDKLoader shared];
    if (loader.loaded) {
        return [NSString stringWithFormat:@"sdk: dynamic=true path=%@", loader.path];
    }
    return [NSString stringWithFormat:@"sdk: dynamic=false, loadError=%@", loader.loadError ?: @"未尝试加载"];
}

/// JSON 字符串里的转义：错误信息可能带引号/换行，不转义会导致解析失败。
+ (NSString *)escape:(NSString *)s {
    NSMutableString *out = [NSMutableString stringWithString:s ?: @""];
    [out replaceOccurrencesOfString:@"\\" withString:@"\\\\" options:0 range:NSMakeRange(0, out.length)];
    [out replaceOccurrencesOfString:@"\"" withString:@"\\\"" options:0 range:NSMakeRange(0, out.length)];
    [out replaceOccurrencesOfString:@"\n" withString:@"\\n" options:0 range:NSMakeRange(0, out.length)];
    [out replaceOccurrencesOfString:@"\r" withString:@"" options:0 range:NSMakeRange(0, out.length)];
    return out;
}

@end
