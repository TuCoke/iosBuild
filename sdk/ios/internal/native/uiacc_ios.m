//go:build ios && cgo

// 控件树采集：把前台应用的 Accessibility 节点摊平成一份 JSON 数组。
//
// 【为什么不用公开 API】iOS 上没有 Android 那种 UI Automator；
// 能拿到别的 App 控件层级的入口只有 Accessibility 私有框架的 AXUIElement* 系列
// （参考实现用的是 AXBootstrap / AXSweepNodesJSON 这层更上层的封装）。
// 这里直接用 AXUIElement 系列：语义等价、参数确定，不用猜封装层的私有约定。
//
// 【可用性】调用方必须有 Accessibility 授权（platform-application + 越狱/TrollStore）；
// 普通签名会拿到 kAXErrorCannotComplete，这里如实返回"不可用"，绝不返回空树冒充成功。
#import "runtime.h"
#import <CoreFoundation/CoreFoundation.h>

typedef CFTypeRef AXUIElementRef;
typedef int AXError;

// 属性名常量在私有框架里是 CFString，这里按名字取，避免链接期依赖。
static CFStringRef AGCopyAttr(const char *name) {
    CFStringRef value = AGSymbol(name);
    if (value) return CFRetain(value);
    return NULL;
}

typedef AXUIElementRef (*AXCreateApp)(pid_t);
typedef AXError (*AXCopyAttr)(AXUIElementRef, CFStringRef, CFTypeRef *);
typedef CFTypeRef (*AXGetAttrRaw)(AXUIElementRef, CFStringRef);

// AGCopyString 读一个字符串属性（取不到返回空串）。
static NSString *AGCopyString(AXUIElementRef element, AXCopyAttr copyAttr, const char *attrName) {
    if (!element || !copyAttr) return @"";
    CFStringRef name = AGCopyAttr(attrName);
    if (!name) return @"";
    CFTypeRef value = NULL;
    NSString *out = @"";
    if (copyAttr(element, name, &value) == 0 && value) {
        if (CFGetTypeID(value) == CFStringGetTypeID()) out = [(NSString *)value copy];
        CFRelease(value);
    }
    CFRelease(name);
    return [out autorelease];
}

// AGCopyBool 读布尔属性；读不到返回 -1（"未知"），不要混成 false。
static int AGCopyBool(AXUIElementRef element, AXCopyAttr copyAttr, const char *attrName) {
    if (!element || !copyAttr) return -1;
    CFStringRef name = AGCopyAttr(attrName);
    if (!name) return -1;
    CFTypeRef value = NULL;
    int out = -1;
    if (copyAttr(element, name, &value) == 0 && value) {
        if (CFGetTypeID(value) == CFBooleanGetTypeID()) out = CFBooleanGetValue((CFBooleanRef)value) ? 1 : 0;
        CFRelease(value);
    }
    CFRelease(name);
    return out;
}

// AGCopyRect 读位置与尺寸（AXValue 里包着 CGPoint/CGSize）。
static CGRect AGCopyRect(AXUIElementRef element, AXCopyAttr copyAttr) {
    CGRect rect = CGRectZero;
    if (!element || !copyAttr) return rect;
    CFStringRef posName = AGCopyAttr("kAXPositionAttribute");
    CFStringRef sizeName = AGCopyAttr("kAXSizeAttribute");
    CFTypeRef posValue = NULL, sizeValue = NULL;
    if (posName && copyAttr(element, posName, &posValue) == 0 && posValue) {
        CGPoint point = CGPointZero;
        if (AXValueGetValue((AXValueRef)posValue, kAXValueCGPointType, &point)) rect.origin = point;
        CFRelease(posValue);
    }
    if (sizeName && copyAttr(element, sizeName, &sizeValue) == 0 && sizeValue) {
        CGSize size = CGSizeZero;
        if (AXValueGetValue((AXValueRef)sizeValue, kAXValueCGSizeType, &size)) rect.size = size;
        CFRelease(sizeValue);
    }
    if (posName) CFRelease(posName);
    if (sizeName) CFRelease(sizeName);
    return rect;
}

// AGCopyChildren 读子节点数组。
static CFArrayRef AGCopyChildren(AXUIElementRef element, AXCopyAttr copyAttr) {
    if (!element || !copyAttr) return NULL;
    CFStringRef name = AGCopyAttr("kAXChildrenAttribute");
    if (!name) return NULL;
    CFTypeRef value = NULL;
    CFArrayRef out = NULL;
    if (copyAttr(element, name, &value) == 0 && value) {
        if (CFGetTypeID(value) == CFArrayGetTypeID()) out = (CFArrayRef)value;
        else CFRelease(value);
    }
    CFRelease(name);
    return out;
}

ag_ios_result AGUIAcc(NSString *op, NSDictionary *args) {
    if (![op isEqualToString:@"uiacc.snapshot"]) return AGError(1,@"未知控件操作");

    AXCreateApp createApp = AGSymbol("AXUIElementCreateApplication");
    AXCopyAttr copyAttr = AGSymbol("AXUIElementCopyAttributeValue");
    if (!createApp || !copyAttr) return AGError(1,@"Accessibility 采集接口不可用（需要 AX 私有框架）");

    int limit = [args[@"limit"] intValue];
    if (limit <= 0 || limit > 8000) limit = 3000;

    // 前台应用的 pid：SBSCopyFrontmostApplicationDisplayIdentifier 只给 bundleId，
    // 这里用 workspace 的 frontmostApplication 拿 pid。
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
    pid_t selfPID = getpid();
    if (frontPID <= 0 || frontPID == selfPID) return AGError(1,@"拿不到前台应用进程（需要 frontboard/workspace 权限）");

    AXUIElementRef app = createApp(frontPID);
    if (!app) return AGError(3,@"创建 AX 根节点失败（多半是没有 Accessibility 授权）");

    NSMutableArray *nodes = [NSMutableArray array];
    NSMutableArray *queue = [NSMutableArray arrayWithObject:(__bridge id)app];
    // 广度优先：保证同一层的节点顺序稳定（选择器按 index 命中时不受深度影响）。
    NSUInteger head = 0;
    int index = 0;
    while (head < queue.count && (int)nodes.count < limit) {
        AXUIElementRef element = (__bridge AXUIElementRef)queue[head];
        head++;
        @autoreleasepool {
            CGRect rect = AGCopyRect(element, copyAttr);
            int enabled = AGCopyBool(element, copyAttr, "kAXEnabledAttribute");
            int selected = AGCopyBool(element, copyAttr, "kAXSelectedAttribute");
            int focused = AGCopyBool(element, copyAttr, "kAXFocusedAttribute");
            NSString *role = AGCopyString(element, copyAttr, "kAXRoleAttribute");
            NSString *text = AGCopyString(element, copyAttr, "kAXTitleAttribute");
            NSString *value = AGCopyString(element, copyAttr, "kAXValueAttribute");
            NSString *desc = AGCopyString(element, copyAttr, "kAXDescriptionAttribute");
            NSString *identifier = AGCopyString(element, copyAttr, "kAXIdentifierAttribute");
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
                @"scrollable": @(value != nil && role.length > 0 && [role containsString:@"Scroll"]),
                @"editable": @([role containsString:@"TextField"] || [role containsString:@"TextView"]),
                @"checked": @(selected == 1),
                @"password": @([role containsString:@"Secure"]),
                @"focused": @(focused == 1),
            }];
            index++;

            if (children) {
                for (CFIndex i = 0; i < CFArrayGetCount(children); i++) {
                    AXUIElementRef child = (AXUIElementRef)CFArrayGetValueAtIndex(children, i);
                    if (child) [queue addObject:(__bridge id)child];
                }
                CFRelease(children);
            }
        }
    }
    CFRelease(app);
    return AGJSON(@{@"nodes": nodes});
}
