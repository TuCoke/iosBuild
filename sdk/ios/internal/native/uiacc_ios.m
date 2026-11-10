//go:build ios && cgo

// 控件树采集：把前台应用的 Accessibility 节点摊平成一份 JSON 数组。
//
// 【为什么不用公开 API】iOS 上没有 Android 那种 UI Automator；
// 能拿到别的 App 控件层级的入口只有 Accessibility 的 AXUIElement* 系列
// （参考实现用的是 AXBootstrap / AXSweepNodesJSON 这层更上层的封装）。
// 这里直接用 AXUIElement 系列：语义等价、参数确定，不用猜封装层的私有约定。
//
// 【为什么不 include 头文件】AXUIElement / AXValue 在 iOS SDK 里没有公开头，
// 全进程内 dlopen + dlsym 解析；属性名用 CFSTR("AXRole") 这类字面量 ——
// kAX* 常量的值就是这些字符串，按值取等价于按符号取，且不依赖框架导出符号名。
//
// 【可用性】必须有 Accessibility 授权（platform-application + 越狱/TrollStore）；
// 普通签名会拿到 kAXErrorCannotComplete，这里如实返回"不可用"，绝不返回空树冒充成功。
#import "runtime.h"
#import <CoreFoundation/CoreFoundation.h>

typedef CFTypeRef AXElement;
typedef CFTypeRef AXValueLocal;
typedef int AXErr;

typedef AXElement (*AXCreateApp)(pid_t);
typedef AXErr (*AXCopyAttr)(AXElement, CFStringRef, CFTypeRef *);
typedef Boolean (*AXValueGetValueFn)(AXValueLocal, int, void *);

// AXValue 的类型编号（与 Apple 的 AXValueType 一致：1=CGPoint 2=CGSize 3=CGRect）。
enum { AGValueCGPoint = 1, AGValueCGSize = 2 };

// AGLoadAccessibility 打开 Accessibility 私有框架，符号才会被解析到。
static void AGLoadAccessibility(void) {
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        dlopen("/System/Library/PrivateFrameworks/Accessibility.framework/Accessibility", RTLD_LAZY | RTLD_GLOBAL);
        dlopen("/System/Library/PrivateFrameworks/AccessibilityUtilities.framework/AccessibilityUtilities", RTLD_LAZY | RTLD_GLOBAL);
        dlopen("/System/Library/PrivateFrameworks/AccessibilityUIUtilities.framework/AccessibilityUIUtilities", RTLD_LAZY | RTLD_GLOBAL);
    });
}

// AGCopyString 读一个字符串属性（取不到返回空串）。
static NSString *AGCopyString(AXElement element, AXCopyAttr copyAttr, CFStringRef attr) {
    if (!element || !copyAttr || !attr) return @"";
    CFTypeRef value = NULL;
    NSString *out = @"";
    if (copyAttr(element, attr, &value) == 0 && value) {
        if (CFGetTypeID(value) == CFStringGetTypeID()) out = [(NSString *)value copy];
        CFRelease(value);
    }
    return [out autorelease];
}

// AGCopyBool 读布尔属性；读不到返回 -1（"未知"），不要混成 false。
static int AGCopyBool(AXElement element, AXCopyAttr copyAttr, CFStringRef attr) {
    if (!element || !copyAttr || !attr) return -1;
    CFTypeRef value = NULL;
    int out = -1;
    if (copyAttr(element, attr, &value) == 0 && value) {
        if (CFGetTypeID(value) == CFBooleanGetTypeID()) out = CFBooleanGetValue((CFBooleanRef)value) ? 1 : 0;
        CFRelease(value);
    }
    return out;
}

// AGCopyRect 读位置与尺寸。值被包在 AXValue 里，用 dlsym 出来的 AXValueGetValue 解开。
static CGRect AGCopyRect(AXElement element, AXCopyAttr copyAttr) {
    CGRect rect = CGRectZero;
    if (!element || !copyAttr) return rect;
    AXValueGetValueFn getValue = AGSymbol("AXValueGetValue");
    if (!getValue) return rect;
    CFTypeRef value = NULL;
    if (copyAttr(element, CFSTR("AXPosition"), &value) == 0 && value) {
        CGPoint point = CGPointZero;
        if (getValue((AXValueLocal)value, AGValueCGPoint, &point)) rect.origin = point;
        CFRelease(value);
    }
    value = NULL;
    if (copyAttr(element, CFSTR("AXSize"), &value) == 0 && value) {
        CGSize size = CGSizeZero;
        if (getValue((AXValueLocal)value, AGValueCGSize, &size)) rect.size = size;
        CFRelease(value);
    }
    return rect;
}

// AGCopyChildren 读子节点数组。
static CFArrayRef AGCopyChildren(AXElement element, AXCopyAttr copyAttr) {
    if (!element || !copyAttr) return NULL;
    CFTypeRef value = NULL;
    CFArrayRef out = NULL;
    if (copyAttr(element, CFSTR("AXChildren"), &value) == 0 && value) {
        if (CFGetTypeID(value) == CFArrayGetTypeID()) out = (CFArrayRef)value;
        else CFRelease(value);
    }
    return out;
}

ag_ios_result AGUIAcc(NSString *op, NSDictionary *args) {
    if (![op isEqualToString:@"uiacc.snapshot"]) return AGError(1,@"未知控件操作");

    AGLoadAccessibility();
    AXCreateApp createApp = AGSymbol("AXUIElementCreateApplication");
    AXCopyAttr copyAttr = AGSymbol("AXUIElementCopyAttributeValue");
    if (!createApp || !copyAttr) return AGError(1,@"Accessibility 采集接口不可用（需要 AX 私有框架）");

    int limit = [args[@"limit"] intValue];
    if (limit <= 0 || limit > 8000) limit = 3000;

    // 前台应用的 pid：workspace 的 frontmostApplication 拿进程号。
    pid_t frontPID = 0;
    Class workspaceClass = NSClassFromString(@"LSApplicationWorkspace");
    SEL defaultSel = NSSelectorFromString(@"defaultWorkspace");
    SEL frontSel = NSSelectorFromString(@"frontmostApplication");
    SEL pidSel = NSSelectorFromString(@"processIdentifier");
    if ([workspaceClass respondsToSelector:defaultSel]) {
        id workspace = ((id(*)(id,SEL))objc_msgSend)(workspaceClass, defaultSel);
        if ([workspace respondsToSelector:frontSel]) {
            id app = ((id(*)(id,SEL))objc_msgSend)(workspace, frontSel);
            if (app && [app respondsToSelector:pidSel]) {
                frontPID = ((pid_t(*)(id,SEL))objc_msgSend)(app, pidSel);
            }
        }
    }
    if (frontPID <= 0 || frontPID == getpid()) return AGError(1,@"拿不到前台应用进程（需要 frontboard/workspace 权限）");

    AXElement app = createApp(frontPID);
    if (!app) return AGError(3,@"创建 AX 根节点失败（多半是没有 Accessibility 授权）");

    NSMutableArray *nodes = [NSMutableArray array];
    NSMutableArray *queue = [NSMutableArray arrayWithObject:(__bridge id)app];
    // 广度优先：同层顺序稳定，选择器按 index 命中时不随深度变化。
    NSUInteger head = 0;
    int index = 0;
    while (head < queue.count && (int)nodes.count < limit) {
        AXElement element = (__bridge AXElement)queue[head];
        head++;
        @autoreleasepool {
            CGRect rect = AGCopyRect(element, copyAttr);
            int enabled = AGCopyBool(element, copyAttr, CFSTR("AXEnabled"));
            int selected = AGCopyBool(element, copyAttr, CFSTR("AXSelected"));
            int focused = AGCopyBool(element, copyAttr, CFSTR("AXFocused"));
            NSString *role = AGCopyString(element, copyAttr, CFSTR("AXRole"));
            NSString *text = AGCopyString(element, copyAttr, CFSTR("AXTitle"));
            NSString *value = AGCopyString(element, copyAttr, CFSTR("AXValue"));
            NSString *desc = AGCopyString(element, copyAttr, CFSTR("AXDescription"));
            NSString *identifier = AGCopyString(element, copyAttr, CFSTR("AXIdentifier"));
            CFArrayRef children = AGCopyChildren(element, copyAttr);
            int childCount = children ? (int)CFArrayGetCount(children) : 0;

            [nodes addObject:@{
                @"text": text ?: @"",
                @"value": (desc.length ? desc : (value ?: @"")) ?: @"",
                @"identifier": identifier ?: @"",
                @"class": role ?: @"",
                @"role": role ?: @"",
                @"bundle": @"",
                @"left": @((int)rect.origin.x), @"top": @((int)rect.origin.y),
                @"right": @((int)(rect.origin.x + rect.size.width)),
                @"bottom": @((int)(rect.origin.y + rect.size.height)),
                @"parentLeft": @(0), @"parentTop": @(0), @"parentRight": @(0), @"parentBottom": @(0),
                @"index": @(index),
                @"childCount": @(childCount),
                @"clickable": @(enabled == 1),
                @"selected": @(selected == 1),
                @"enabled": @(enabled != 0),
                @"scrollable": @([role containsString:@"Scroll"]),
                @"editable": @([role containsString:@"TextField"] || [role containsString:@"TextView"]),
                @"checked": @(selected == 1),
                @"password": @([role containsString:@"Secure"]),
                @"focused": @(focused == 1),
            }];
            index++;

            if (children) {
                for (CFIndex i = 0; i < CFArrayGetCount(children); i++) {
                    AXElement child = (AXElement)CFArrayGetValueAtIndex(children, i);
                    if (child) [queue addObject:(__bridge id)child];
                }
                CFRelease(children);
            }
        }
    }
    CFRelease(app);
    return AGJSON(@{@"nodes": nodes});
}
