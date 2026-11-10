//go:build ios && cgo

// 控件树采集（AX）—— 与参考实现同名同签名的两个入口。
//
// 反汇编确认的签名（逆向自 libgoeval.dylib）：
//
//	int   AXBootstrap(void);                          // 0 成功 / -1 未就绪
//	char *AXSweepNodesJSON(char *buf, size_t bufLen); // 成功返回 malloc 的 JSON，
//	                                                   // 失败把错误写进 buf 并返回 NULL
//
// 参考实现把这两个函数同时编进了壳和 dylib（壳自己也用），我们也照做：
// 这里的实现是 C 函数，Go 侧通过 cgo 调，壳可以 dlsym 调。
#import "runtime.h"
#include <string.h>
#include <stdio.h>
#include <unistd.h>

// ---- AX 基础类型与本文件的私有助手 ----
// 与 uiacc_ios.m 里的同名 typedef 各自独立（都是文件内可见）。
typedef CFTypeRef AXElement;
typedef int AXErr;
typedef AXElement (*AXCreateApp)(pid_t);
typedef AXErr (*AXCopyAttr)(AXElement, CFStringRef, CFTypeRef *);
typedef Boolean (*AXValueGetValueFn)(CFTypeRef, int, void *);

static NSString *AGCopyAXString(AXElement el, AXCopyAttr copyAttr, CFStringRef attr) {
    if (!el || !copyAttr || !attr) return @"";
    CFTypeRef v = NULL;
    NSString *out = @"";
    if (copyAttr(el, attr, &v) == 0 && v) {
        if (CFGetTypeID(v) == CFStringGetTypeID()) out = [(NSString *)v copy];
        CFRelease(v);
    }
    return [out autorelease];
}

static int AGCopyAXBool(AXElement el, AXCopyAttr copyAttr, CFStringRef attr) {
    if (!el || !copyAttr || !attr) return -1;
    CFTypeRef v = NULL;
    int out = -1;
    if (copyAttr(el, attr, &v) == 0 && v) {
        if (CFGetTypeID(v) == CFBooleanGetTypeID()) out = CFBooleanGetValue((CFBooleanRef)v) ? 1 : 0;
        CFRelease(v);
    }
    return out;
}

static CGRect AGCopyAXRect(AXElement el, AXCopyAttr copyAttr) {
    CGRect rect = CGRectZero;
    AXValueGetValueFn getValue = (AXValueGetValueFn)AGSymbol("AXValueGetValue");
    if (!el || !copyAttr || !getValue) return rect;
    CFTypeRef v = NULL;
    if (copyAttr(el, CFSTR("AXPosition"), &v) == 0 && v) {
        CGPoint p = CGPointZero;
        if (getValue(v, 1, &p)) rect.origin = p;
        CFRelease(v);
    }
    v = NULL;
    if (copyAttr(el, CFSTR("AXSize"), &v) == 0 && v) {
        CGSize sz = CGSizeZero;
        if (getValue(v, 2, &sz)) rect.size = sz;
        CFRelease(v);
    }
    return rect;
}

static CFArrayRef AGCopyAXChildren(AXElement el, AXCopyAttr copyAttr) {
    if (!el || !copyAttr) return NULL;
    CFTypeRef v = NULL;
    CFArrayRef out = NULL;
    if (copyAttr(el, CFSTR("AXChildren"), &v) == 0 && v) {
        if (CFGetTypeID(v) == CFArrayGetTypeID()) out = (CFArrayRef)v;
        else CFRelease(v);
    }
    return out;
}

// AX 是否可用：AXUIElement 类存在才算。
static BOOL gAXReady;
static dispatch_once_t gAXOnce;

// AXBootstrap 初始化 AX 采集。成功返回 0，失败 -1。
int AXBootstrap(void) {
    dispatch_once(&gAXOnce, ^{
        // 参考实现的做法：AXUIElement 类存在（AXRuntime 已加载）才算可用。
        // 这里先 dlopen AXRuntime，再判断类是否存在 —— 与它的错误串一致：
        // "AX bootstrap failed (AXUIElement class unavailable)"
        dlopen("/System/Library/PrivateFrameworks/AXRuntime.framework/AXRuntime", RTLD_LAZY);
        gAXReady = NSClassFromString(@"AXUIElement") != nil ||
                   AGSymbol("AXUIElementCreateApplication") != NULL;
    });
    return gAXReady ? 0 : -1;
}

// AGCollectAXNodes 广度优先遍历前台应用的 AX 节点，返回 JSON 字符串（malloc）。
//
// 这是"我们的实现"，不假设参考实现的内部结构；对外只保证
// AXSweepNodesJSON 的约定：成功返回 malloc 串、失败写 buf 返回 NULL。
static char *AGCollectAXNodes(char *buf, size_t bufLen) {
    AXCreateApp createApp = (AXCreateApp)AGSymbol("AXUIElementCreateApplication");
    AXCopyAttr copyAttr = (AXCopyAttr)AGSymbol("AXUIElementCopyAttributeValue");
    if (!createApp || !copyAttr) {
        if (buf && bufLen) snprintf(buf, bufLen, "%s", "AX bootstrap failed (AXUIElement class unavailable)");
        return NULL;
    }

    // 前台应用 pid
    pid_t pid = 0;
    Class ws = NSClassFromString(@"LSApplicationWorkspace");
    SEL def = NSSelectorFromString(@"defaultWorkspace");
    SEL front = NSSelectorFromString(@"frontmostApplication");
    SEL pidSel = NSSelectorFromString(@"processIdentifier");
    if ([ws respondsToSelector:def]) {
        id workspace = ((id(*)(id,SEL))objc_msgSend)(ws, def);
        if ([workspace respondsToSelector:front]) {
            id app = ((id(*)(id,SEL))objc_msgSend)(workspace, front);
            if (app && [app respondsToSelector:pidSel]) {
                pid = ((pid_t(*)(id,SEL))objc_msgSend)(app, pidSel);
            }
        }
    }
    if (pid <= 0 || pid == getpid()) {
        if (buf && bufLen) snprintf(buf, bufLen, "%s", "foreground app unavailable");
        return NULL;
    }

    AXElement app = createApp(pid);
    if (!app) {
        if (buf && bufLen) snprintf(buf, bufLen, "%s", "AX element for foreground app is NULL");
        return NULL;
    }

    NSMutableArray *nodes = [NSMutableArray array];
    NSMutableArray *queue = [NSMutableArray arrayWithObject:(__bridge id)app];
    NSUInteger head = 0;
    int index = 0;
    while (head < queue.count && nodes.count < 3000) {
        AXElement element = (__bridge AXElement)queue[head];
        head++;
        @autoreleasepool {
            CGRect rect = AGCopyAXRect(element, copyAttr);
            int enabled = AGCopyAXBool(element, copyAttr, CFSTR("AXEnabled"));
            int selected = AGCopyAXBool(element, copyAttr, CFSTR("AXSelected"));
            NSString *role = AGCopyAXString(element, copyAttr, CFSTR("AXRole"));
            NSString *text = AGCopyAXString(element, copyAttr, CFSTR("AXTitle"));
            NSString *desc = AGCopyAXString(element, copyAttr, CFSTR("AXDescription"));
            NSString *value = AGCopyAXString(element, copyAttr, CFSTR("AXValue"));
            NSString *identifier = AGCopyAXString(element, copyAttr, CFSTR("AXIdentifier"));
            CFArrayRef children = AGCopyAXChildren(element, copyAttr);
            [nodes addObject:@{
                @"text": text ?: @"",
                @"desc": (desc.length ? desc : (value ?: @"")) ?: @"",
                @"id": identifier ?: @"",
                @"className": role ?: @"",
                @"bounds": @[@((int)rect.origin.x), @((int)rect.origin.y),
                            @((int)(rect.origin.x + rect.size.width)),
                            @((int)(rect.origin.y + rect.size.height))],
                @"index": @(index),
                @"childCount": @(children ? (int)CFArrayGetCount(children) : 0),
                @"clickable": @((BOOL)(enabled != 0)),
                @"selected": @((BOOL)(selected != 0)),
                @"enabled": @((BOOL)(enabled != 0)),
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

    NSData *json = [NSJSONSerialization dataWithJSONObject:nodes options:0 error:NULL];
    if (!json) {
        if (buf && bufLen) snprintf(buf, bufLen, "%s", "JSON serialize failed: node array");
        return NULL;
    }
    size_t len = json.length;
    char *out = malloc(len + 1);
    if (!out) {
        if (buf && bufLen) snprintf(buf, bufLen, "malloc %zu failed", len + 1);
        return NULL;
    }
    memcpy(out, json.bytes, len);
    out[len] = '\0';
    return out;
}

// AXSweepNodesJSON 与参考实现同签名同语义。
char *AXSweepNodesJSON(char *buf, size_t bufLen) {
    @autoreleasepool {
        if (AXBootstrap() != 0) {
            if (buf && bufLen) snprintf(buf, bufLen, "%s", "AX bootstrap failed (AXUIElement class unavailable)");
            return NULL;
        }
        return AGCollectAXNodes(buf, bufLen);
    }
}
