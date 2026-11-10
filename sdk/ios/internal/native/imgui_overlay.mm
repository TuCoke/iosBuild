// imgui_overlay.mm —— 悬浮层（HUD / Console）用 imgui + Metal 渲染
//
// 【为什么不用 UIKit 了】
// 参考实现的悬浮层是 imgui 画的（它的 dylib 依赖里有 libimgui），
// 好处是：绘制不进主界面的视图树、不受宿主 UI 布局影响、每帧全权控制。
// 我们照它的做法：一个独立的透明 UIWindow + MTKView，imgui 在里面画。
//
// 【关键点】
// 1) overlay window 必须是 UIWindowLevelAlert 级别的独立 window，
//    否则会跟着主界面一起被布局/滚动。
// 2) 默认 userInteractionEnabled = NO，避免挡住下面的点击；
//    只有 HUD 需要手动交互时才打开。
// 3) imgui 的坐标是"点"（points），不是像素：Retina 上要除以 scale。
#import "runtime.h"
#import <Metal/Metal.h>
#import <MetalKit/MetalKit.h>
#import <QuartzCore/CADisplayLink.h>
#import "imgui.h"
#import "backends/imgui_impl_metal.h"

@interface AGImGuiOverlayView : MTKView <MTKViewDelegate>
@property (nonatomic, assign) BOOL rendering;
@end

@implementation AGImGuiOverlayView
- (instancetype)initWithFrame:(CGRect)frame device:(id<MTLDevice>)device {
    self = [super initWithFrame:frame device:device];
    if (self) {
        self.delegate = self;
        self.opaque = NO;
        self.backgroundColor = UIColor.clearColor;
        self.clearColor = MTLClearColorMake(0, 0, 0, 0);
        self.framebufferOnly = NO;
        self.enableSetNeedsDisplay = NO;
        self.paused = NO;
    }
    return self;
}
- (void)mtkView:(MTKView *)view drawableSizeWillChange:(CGSize)size {}
- (void)drawInMTKView:(MTKView *)view {
    if (!self.rendering) {
        return;
    }
    // 悬浮层没有内容时直接跳过，别空转耗电
    extern void AGImGuiRenderFrame(MTKView *view);
    AGImGuiRenderFrame(view);
}
@end

// ---- 悬浮层状态 ----

typedef struct {
    BOOL visible;
    float x, y, w, h;       // points
    float fontSize;
    NSString *background;   // #RRGGBB
    NSMutableArray<NSString *> *lines;
} AGPanel;

static AGImGuiOverlayView *gOverlayView;   // 提前声明：下面的 CADisplayLink target 要用
static UIWindow *gOverlayWindow;

/// CADisplayLink 的 target：只做一件事——让 MTKView 每帧重绘
@interface AGImGuiOverlayDriver : NSObject
+ (instancetype)shared;
- (void)tick;
@end

@implementation AGImGuiOverlayDriver
+ (instancetype)shared {
    static AGImGuiOverlayDriver *shared = nil;
    static dispatch_once_t once;
    dispatch_once(&once, ^{ shared = [[AGImGuiOverlayDriver alloc] init]; });
    return shared;
}
- (void)tick { [gOverlayView setNeedsDisplay]; }
@end

static id<MTLCommandQueue> gCommandQueue;
static CADisplayLink *gDisplayLink;
static NSMutableDictionary<NSNumber *, AGPanel *> *gHudPanels;      // id → 面板
static NSMutableDictionary<NSNumber *, AGPanel *> *gConsolePanels;

static inline AGPanel *AGPanelOf(NSMutableDictionary *store, NSNumber *key) {
    return store[key];
}

static AGPanel *AGPanelCreate(NSMutableDictionary *store, NSNumber *key) {
    AGPanel *p = [[AGPanel alloc] init];
    store[key] = p;
    return p;
}

static void AGPanelDestroy(NSMutableDictionary *store, NSNumber *key) {
    [store removeObjectForKey:key];
}

/// 十六进制颜色 → UIColor（imgui 用 0xRRGGBBAA 的 ImU32）
static ImU32 AGColorFromHex(NSString *hex, CGFloat alpha) {
    unsigned int rgb = 0;
    if (hex.length >= 6) {
        NSScanner *scanner = [NSScanner scannerWithString:[hex substringFromIndex:hex.length >= 7 ? 1 : 0]];
        [scanner scanHexInt:&rgb];
    }
    return IM_COL32((rgb >> 16) & 0xFF, (rgb >> 8) & 0xFF, rgb & 0xFF, (int)(alpha * 255));
}

// ---- 初始化 ----

static void AGImGuiEnsureReady(void) {
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        gHudPanels = [NSMutableDictionary dictionary];
        gConsolePanels = [NSMutableDictionary dictionary];

        id<MTLDevice> device = MTLCreateSystemDefaultDevice();
        if (device == nil) {
            return; // 模拟器/无 Metal 设备：悬浮层不可用，但不影响其它功能
        }
        gCommandQueue = [device newCommandQueue];

        UIWindowScene *scene = nil;
        for (UIScene *s in UIApplication.sharedApplication.connectedScenes) {
            if ([s isKindOfClass:UIWindowScene.class]) {
                scene = (UIWindowScene *)s;
                break;
            }
        }
        if (scene == nil) {
            return;
        }
        gOverlayWindow = [[UIWindow alloc] initWithWindowScene:scene];
        gOverlayWindow.windowLevel = UIWindowLevelAlert - 1; // 在 Alert 之下，避免挡住弹窗
        gOverlayWindow.backgroundColor = UIColor.clearColor;
        gOverlayWindow.userInteractionEnabled = NO;          // 默认不挡触摸
        gOverlayWindow.rootViewController = [[UIViewController alloc] init];
        gOverlayWindow.rootViewController.view.backgroundColor = UIColor.clearColor;

        gOverlayView = [[AGImGuiOverlayView alloc] initWithFrame:gOverlayWindow.bounds device:device];
        gOverlayView.autoresizingMask = UIViewAutoresizingFlexibleWidth | UIViewAutoresizingFlexibleHeight;
        [gOverlayWindow.rootViewController.view addSubview:gOverlayView];
        gOverlayWindow.hidden = NO;

        IMGUI_CHECKVERSION();
        ImGui::CreateContext();
        ImGuiIO &io = ImGui::GetIO();
        io.IniFilename = NULL;   // 不写 imgui.ini 到沙盒
        io.LogFilename = NULL;
        ImGui_ImplMetal_Init(device);

        // CADisplayLink 需要一个真实 target：用 block 冒充 selector 会崩
        gDisplayLink = [CADisplayLink displayLinkWithTarget:[AGImGuiOverlayDriver shared]
                                                   selector:@selector(tick)];
        [gDisplayLink addToRunLoop:NSRunLoop.mainRunLoop forMode:NSRunLoopCommonModes];
    });
}

/// 每帧渲染：把当前所有可见面板画出来
void AGImGuiRenderFrame(MTKView *view) {
    if (gOverlayView == nil || gCommandQueue == nil) {
        return;
    }
    MTLRenderPassDescriptor *rpd = view.currentRenderPassDescriptor;
    if (rpd == nil) {
        return;
    }
    ImGui_ImplMetal_NewFrame(rpd);
    ImGui::NewFrame();

    CGFloat scale = UIScreen.mainScreen.scale;
    CGFloat screenH = UIScreen.mainScreen.bounds.size.height;

    // imgui 的 y 轴向下、原点是左上，和 UIKit 一致；但坐标用 points
    for (NSNumber *key in gHudPanels) {
        AGPanel *p = AGPanelOf(gHudPanels, key);
        if (p == NULL || !p.visible) {
            continue;
        }
        ImGui::SetNextWindowPos(ImVec2(p.x, p.y), ImGuiCond_Always);
        ImGui::SetNextWindowSize(ImVec2(p.w > 0 ? p.w : 240, p.h > 0 ? p.h : 120), ImGuiCond_Always);
        ImGui::PushStyleColor(ImGuiCol_WindowBg, AGColorFromHex(p.background ?: @"#000000", 0.7));
        ImGui::Begin([NSString stringWithFormat:@"hud#%@", key].UTF8String, NULL,
                     ImGuiWindowFlags_NoTitleBar | ImGuiWindowFlags_NoResize |
                     ImGuiWindowFlags_NoMove | ImGuiWindowFlags_NoScrollbar |
                     ImGuiWindowFlags_NoSavedSettings | ImGuiWindowFlags_NoBringToFrontOnFocus);
        ImGui::SetWindowFontScale(p.fontSize / 14.0);
        for (NSString *line in p.lines) {
            ImGui::TextUnformatted(line.UTF8String);
        }
        ImGui::End();
        ImGui::PopStyleColor();
    }

    for (NSNumber *key in gConsolePanels) {
        AGPanel *p = AGPanelOf(gConsolePanels, key);
        if (p == NULL || !p.visible) {
            continue;
        }
        ImGui::SetNextWindowPos(ImVec2(p.x, p.y), ImGuiCond_Always);
        ImGui::SetNextWindowSize(ImVec2(p.w > 0 ? p.w : 320, p.h > 0 ? p.h : 200), ImGuiCond_Always);
        ImGui::PushStyleColor(ImGuiCol_WindowBg, AGColorFromHex(p.background ?: @"#101010", 0.85));
        ImGui::Begin([NSString stringWithFormat:@"console#%@", key].UTF8String, NULL,
                     ImGuiWindowFlags_NoTitleBar | ImGuiWindowFlags_NoResize |
                     ImGuiWindowFlags_NoMove | ImGuiWindowFlags_NoSavedSettings);
        ImGui::SetWindowFontScale(p.fontSize / 14.0);
        for (NSString *line in p.lines) {
            ImGui::TextUnformatted(line.UTF8String);
        }
        if (ImGui::GetScrollY() >= ImGui::GetScrollMaxY() - 1) {
            ImGui::SetScrollHereY(1.0f); // 贴在底部自动滚动
        }
        ImGui::End();
        ImGui::PopStyleColor();
    }
    (void)scale;
    (void)screenH;

    ImGui::Render();
    id<MTLCommandBuffer> cb = [gCommandQueue commandBuffer];
    [cb addCompletedHandler:^(id<MTLCommandBuffer> buffer) {
        ImGui_ImplMetal_NewFrame(rpd); // 释放上一帧资源
    }];
    id<MTLRenderCommandEncoder> enc = [cb renderCommandEncoderWithDescriptor:rpd];
    ImGui_ImplMetal_RenderDrawData(ImGui::GetDrawData(), cb, enc);
    [enc endEncoding];
    [cb presentDrawable:view.currentDrawable];
    [cb commit];
}

// ---- 对外：与 overlay_ios.m 同一套 op ----

static ag_ios_result AGOverlayResult(NSDictionary *payload) {
    return AGJSON(payload ?: @{});
}

ag_ios_result AGImGuiOverlay(NSString *op, NSDictionary *args) {
    AGImGuiEnsureReady();
    if (gOverlayWindow == nil) {
        return AGError(1, @"悬浮层不可用：没有 Metal 设备或 WindowScene");
    }
    NSNumber *key = @([args[@"id"] intValue]);

    if ([op hasPrefix:@"hud."]) {
        if ([op isEqualToString:@"hud.destroy"]) {
            AGPanelDestroy(gHudPanels, key);
            return AGOverlayResult(@{@"ok": @YES});
        }
        AGPanel *p = AGPanelOf(gHudPanels, key);
        if (p == NULL) {
            p = AGPanelCreate(gHudPanels, key);
        }
        if ([op isEqualToString:@"hud.hide"]) {
            p.visible = NO;
            return AGOverlayResult(@{@"ok": @YES});
        }
        if ([op isEqualToString:@"hud.visible"]) {
            return AGOverlayResult(@{@"visible": @(p.visible)});
        }
        // hud.show：位置/尺寸/文本/颜色/字号都可能带
        if (args[@"x1"] != nil) { p.x = [args[@"x1"] floatValue]; }
        if (args[@"y1"] != nil) { p.y = [args[@"y1"] floatValue]; }
        if (args[@"x2"] != nil) { p.w = [args[@"x2"] floatValue] - p.x; }
        if (args[@"y2"] != nil) { p.h = [args[@"y2"] floatValue] - p.y; }
        if (args[@"textSize"] != nil) { p.fontSize = [args[@"textSize"] floatValue]; }
        if (args[@"background"] != nil) { p.background = args[@"background"]; }
        if ([args[@"items"] isKindOfClass:NSArray.class]) {
            [p.lines removeAllObjects];
            for (id item in args[@"items"]) {
                if ([item isKindOfClass:NSDictionary.class]) {
                    NSString *text = item[@"Text"] ?: item[@"text"] ?: @"";
                    [p.lines addObject:text];
                } else if ([item isKindOfClass:NSString.class]) {
                    [p.lines addObject:item];
                }
            }
        }
        p.visible = YES;
        return AGOverlayResult(@{@"ok": @YES});
    }

    if ([op hasPrefix:@"console."]) {
        if ([op isEqualToString:@"console.destroy"]) {
            AGPanelDestroy(gConsolePanels, key);
            return AGOverlayResult(@{@"ok": @YES});
        }
        AGPanel *p = AGPanelOf(gConsolePanels, key);
        if (p == NULL) {
            p = AGPanelCreate(gConsolePanels, key);
        }
        if ([op isEqualToString:@"console.hide"]) {
            p.visible = NO;
            return AGOverlayResult(@{@"ok": @YES});
        }
        if ([op isEqualToString:@"console.visible"]) {
            return AGOverlayResult(@{@"visible": @(p.visible)});
        }
        if ([op isEqualToString:@"console.clear"]) {
            [p.lines removeAllObjects];
            return AGOverlayResult(@{@"ok": @YES});
        }
        if ([op isEqualToString:@"console.print"]) {
            NSString *text = args[@"text"] ?: @"";
            [p.lines addObject:text];
            if (p.lines.count > 500) {
                [p.lines removeObjectsInRange:NSMakeRange(0, p.lines.count - 500)];
            }
            p.visible = YES;
            return AGOverlayResult(@{@"ok": @YES});
        }
        if (args[@"x"] != nil) { p.x = [args[@"x"] floatValue]; }
        if (args[@"y"] != nil) { p.y = [args[@"y"] floatValue]; }
        if (args[@"width"] != nil) { p.w = [args[@"width"] floatValue]; }
        if (args[@"height"] != nil) { p.h = [args[@"height"] floatValue]; }
        if (args[@"textSize"] != nil) { p.fontSize = [args[@"textSize"] floatValue]; }
        if (args[@"background"] != nil) { p.background = args[@"background"]; }
        p.visible = YES;
        return AGOverlayResult(@{@"ok": @YES});
    }

    return AGError(2, [NSString stringWithFormat:@"未知悬浮层方法: %@", op]);
}

/// 悬浮层是否可用：模拟器/老设备可能没有 Metal 设备
BOOL AGImGuiOverlayAvailable(void) {
    AGImGuiEnsureReady();
    return gOverlayWindow != nil && gOverlayView != nil;
}
