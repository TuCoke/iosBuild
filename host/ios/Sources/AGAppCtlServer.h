#import <Foundation/Foundation.h>

// 与参考样本相同的 8990 二进制通道。JS 执行使用 JavaScriptCore，而非 Android Rhino。
@interface AGAppCtlServer : NSObject
+ (instancetype)shared;
- (BOOL)start:(NSError **)error;
- (void)emitLifecycle:(NSString *)name;
@end
