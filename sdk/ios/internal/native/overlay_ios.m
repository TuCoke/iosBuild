//go:build ios && cgo

// 设备端悬浮层：HUD（文字面板）与控制台（滚动日志窗）。
//
// 每个 HUD/Console 对应一个独立 UIWindow，windowLevel 高于普通界面但低于系统弹窗，
// 这样它浮在别的 App 之上、又不挡住权限弹窗。
//
// 【为什么不用 imgui】参考实现用 imgui 自绘（1.8 MB dylib + 4835 个绑定），
// 而这里要的只是"几行文字"和"一块滚动日志"——UIKit 的 UILabel/UITextView 就够，
// 而且不用为它维护一整套 C++ 绑定。
#import "runtime.h"

@interface AGOverlayWindow : UIWindow
@end
@implementation AGOverlayWindow
// 悬浮层不能抢焦点：所有触摸直接穿透给下面的应用。
- (BOOL)pointInside:(CGPoint)point withEvent:(UIEvent *)event { return NO; }
@end

// AGOverlayRegistry 保存 id → 窗口/内容视图，Destroy 时按 id 找回来。
static NSMutableDictionary *AGOverlayRegistry(void) {
    static NSMutableDictionary *registry;
    static dispatch_once_t once;
    dispatch_once(&once, ^{ registry = [NSMutableDictionary new]; });
    return registry;
}

static NSString *AGOverlayKey(NSString *kind, id identifier) {
    NSString *suffix = identifier ? [identifier description] : @"";
    return [NSString stringWithFormat:@"%@:%@", kind, suffix];
}

static UIColor *AGOverlayColor(NSString *hex, UIColor *fallback) {
    if (![hex isKindOfClass:[NSString class]] || hex.length < 7) return fallback;
    unsigned int value = 0;
    NSScanner *scanner = [NSScanner scannerWithString:[hex stringByReplacingOccurrencesOfString:@"#" withString:@""]];
    if (![scanner scanHexInt:&value]) return fallback;
    // 支持 #RRGGBB 与 #AARRGGBB 两种写法（后者 alpha 在高位）。
    if (hex.length >= 9) {
        return [UIColor colorWithRed:((value >> 16) & 0xFF) / 255.0
                               green:((value >> 8) & 0xFF) / 255.0
                                blue:(value & 0xFF) / 255.0
                               alpha:((value >> 24) & 0xFF) / 255.0];
    }
    return [UIColor colorWithRed:((value >> 16) & 0xFF) / 255.0
                           green:((value >> 8) & 0xFF) / 255.0
                            blue:(value & 0xFF) / 255.0
                           alpha:1.0];
}

static AGOverlayWindow *AGOverlayCreate(NSString *key, CGRect frame, UIColor *background) {
    UIWindow *host = UIApplication.sharedApplication.keyWindow;
    AGOverlayWindow *window = [[AGOverlayWindow alloc] initWithFrame:frame];
    window.windowLevel = UIWindowLevelAlert - 1;
    window.backgroundColor = background;
    window.rootViewController = [[[UIViewController alloc] init] autorelease];
    if (host) window.windowScene = host.windowScene; // iOS 13+ 必须挂在同一个 scene 上
    window.hidden = NO;
    AGOverlayRegistry()[key] = window;
    return window;
}

ag_ios_result AGOverlay(NSString *op, NSDictionary *args) {
    NSString *kind = [op hasPrefix:@"hud."] ? @"hud" : @"console";
    NSString *key = AGOverlayKey(kind, args[@"id"]);

    if ([op hasSuffix:@"show"] || [op hasSuffix:@"print"] || [op hasSuffix:@"clear"]) {
        __block ag_ios_result result = AGJSON(@{@"ok":@YES});
        // UI 操作必须在主线程；这里不等太久（创建窗口很快），用 AGOnMain 足够。
        result = AGOnMain(^{
            AGOverlayWindow *window = AGOverlayRegistry()[key];
            CGFloat x = [args[@"x"] doubleValue], y = [args[@"y"] doubleValue];
            CGFloat w = [args[@"width"] doubleValue], h = [args[@"height"] doubleValue];
            if ([kind isEqualToString:@"hud"]) {
                x = [args[@"x1"] doubleValue]; y = [args[@"y1"] doubleValue];
                w = [args[@"x2"] doubleValue] - x; h = [args[@"y2"] doubleValue] - y;
            }
            if (w <= 0) w = 260;
            if (h <= 0) h = [kind isEqualToString:@"hud"] ? 60 : 220;
            CGRect frame = CGRectMake(x, y, w, h);

            if (!window) {
                window = AGOverlayCreate(key, frame, AGOverlayColor(args[@"background"], [UIColor colorWithWhite:0 alpha:0.6]));
            } else {
                window.frame = frame;
                window.hidden = NO;
            }

            if ([kind isEqualToString:@"hud"]) {
                UILabel *label = window.rootViewController.view.subviews.firstObject;
                if (!label) {
                    label = [[[UILabel alloc] initWithFrame:window.bounds] autorelease];
                    label.numberOfLines = 0;
                    [window.rootViewController.view addSubview:label];
                }
                NSArray *items = args[@"items"];
                NSMutableString *text = [NSMutableString string];
                NSString *firstColor = nil;
                if ([items isKindOfClass:[NSArray class]]) {
                    for (NSDictionary *item in items) {
                        if (item[@"Text"]) [text appendFormat:@"%@\n", item[@"Text"]];
                        else if (item[@"text"]) [text appendFormat:@"%@\n", item[@"text"]];
                        if (!firstColor) firstColor = item[@"TextColor"] ?: item[@"textColor"];
                    }
                }
                label.text = text;
                label.textColor = AGOverlayColor(firstColor, UIColor.whiteColor);
                double size = [args[@"textSize"] doubleValue];
                label.font = [UIFont systemFontOfSize:size > 0 ? size : 14];
                label.frame = window.bounds;
                return AGJSON(@{@"ok":@YES});
            }

            UITextView *textView = window.rootViewController.view.subviews.firstObject;
            if (![textView isKindOfClass:[UITextView class]]) {
                textView = [[[UITextView alloc] initWithFrame:window.bounds] autorelease];
                textView.editable = NO;
                textView.backgroundColor = UIColor.clearColor;
                [window.rootViewController.view addSubview:textView];
            }
            double size = [args[@"textSize"] doubleValue];
            textView.font = [UIFont fontWithName:@"Menlo" size:size > 0 ? size : 12] ?: [UIFont systemFontOfSize:12];
            textView.textColor = AGOverlayColor(args[@"textColor"], UIColor.whiteColor);
            textView.frame = window.bounds;
            if ([op hasSuffix:@"clear"]) {
                textView.text = @"";
            } else if (args[@"text"]) {
                textView.text = [textView.text stringByAppendingFormat:@"%@\n", args[@"text"]];
                [textView scrollRangeToVisible:NSMakeRange(textView.text.length, 0)];
            } else if ([args[@"lines"] isKindOfClass:[NSArray class]]) {
                textView.text = [args[@"lines"] componentsJoinedByString:@"\n"];
            }
            return AGJSON(@{@"ok":@YES});
        });
        return result;
    }

    if ([op hasSuffix:@"visible"]) {
        AGOverlayWindow *window = AGOverlayRegistry()[key];
        return AGJSON(@{@"visible":@((BOOL)(window != nil && !window.hidden))});
    }

    if ([op hasSuffix:@"hide"] || [op hasSuffix:@"destroy"]) {
        NSString *op2 = op;
        BOOL destroy = [op2 hasSuffix:@"destroy"];
        return AGOnMain(^{
            AGOverlayWindow *window = AGOverlayRegistry()[key];
            if (!window) return AGJSON(@{@"ok":@NO});
            if (destroy) {
                window.hidden = YES;
                [AGOverlayRegistry() removeObjectForKey:key];
            } else {
                window.hidden = YES;
            }
            return AGJSON(@{@"ok":@YES});
        });
    }

    return AGError(1,@"未知悬浮层操作");
}
