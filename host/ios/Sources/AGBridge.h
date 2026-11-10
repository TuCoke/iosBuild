// AGBridge.h —— 壳调用 SDK 的唯一入口
//
// 为什么要这个薄壳：调用点全部集中在一处，出错时好加日志、好替换实现；
// 也避免各处直接拿 dlsym 函数指针。
#import <Foundation/Foundation.h>

NS_ASSUME_NONNULL_BEGIN

@interface AGBridge : NSObject

/// 调一次 SDK。payload 传 nil 时按 "{}" 处理。失败返回 nil。
+ (nullable NSString *)invoke:(NSString *)method payload:(nullable NSString *)payload;

/// 当前 SDK 状态：dynamic=true/false、path、loadError
+ (NSString *)statusLine;

@end

NS_ASSUME_NONNULL_END
