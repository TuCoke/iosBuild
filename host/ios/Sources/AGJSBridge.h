#import <Foundation/Foundation.h>
#import <JavaScriptCore/JavaScriptCore.h>

NS_ASSUME_NONNULL_BEGIN
// 每个 JSContext 独享控件、回调及窗口；断开连接后主动销毁。
@interface AGJSBridge : NSObject
- (instancetype)initWithContext:(JSContext *)context queue:(dispatch_queue_t)queue;
- (void)install;
- (void)close;
@end
NS_ASSUME_NONNULL_END
