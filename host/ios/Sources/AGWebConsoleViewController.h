// AGWebConsoleViewController.h —— 内嵌 Web 控制台
#import <UIKit/UIKit.h>

NS_ASSUME_NONNULL_BEGIN

@interface AGWebConsoleViewController : UIViewController

/// 用指定地址创建。地址来自 localapi.start 返回的 url（http://127.0.0.1:18989/ui/index.html）
- (instancetype)initWithURL:(NSURL *)url;

@end

NS_ASSUME_NONNULL_END
