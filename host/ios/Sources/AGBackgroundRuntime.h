#import <Foundation/Foundation.h>

NS_ASSUME_NONNULL_BEGIN
// 宿主统一后台运行需求管理；不属于设备 SDK，也不承诺系统永不挂起进程。
@interface AGBackgroundRuntime : NSObject
+ (instancetype)shared;
- (void)install;
- (NSDictionary *)command:(NSString *)command owner:(NSString *)owner enabled:(BOOL)enabled;
@end
NS_ASSUME_NONNULL_END
