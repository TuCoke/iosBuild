//go:build ios && cgo

#import "runtime.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static BOOL axPreviousEnabled,axPreviousKnown,axConfigured,axPrepared,axBaselineCaptured;
static NSRecursiveLock *axEnvironmentLock;
static void (*axSetEnabled)(Boolean);
static Boolean (*axGetEnabled)(void);
static NSString *axPreparationMode;
static void AGAXPrepareEnvironment(void){
    axSetEnabled=(void(*)(Boolean))dlsym(RTLD_DEFAULT,"_AXSFullKeyboardAccessSetEnabled");
    axGetEnabled=(Boolean(*)(void))dlsym(RTLD_DEFAULT,"_AXSFullKeyboardAccessEnabled");
    if(axSetEnabled && axGetEnabled){
        BOOL current=axGetEnabled();
        if(!axBaselineCaptured){axPreviousEnabled=current;axPreviousKnown=YES;}
        // 首次引导或显式恢复后的重新引导才刷新，不能每次采集都切换全局设置。
        if(current){axSetEnabled(false);usleep(100000);}
        axSetEnabled(true);axConfigured=axGetEnabled();axPreparationMode=@"private_api";
    }else{
        CFPropertyListRef previous=CFPreferencesCopyValue(CFSTR("FullKeyboardAccessEnabled"),CFSTR("com.apple.Accessibility"),kCFPreferencesCurrentUser,kCFPreferencesAnyHost);
        if(previous){if(!axBaselineCaptured && CFGetTypeID(previous)==CFBooleanGetTypeID()){axPreviousKnown=YES;axPreviousEnabled=CFBooleanGetValue((CFBooleanRef)previous);}CFRelease(previous);}
        CFPreferencesSetValue(CFSTR("FullKeyboardAccessEnabled"),kCFBooleanTrue,CFSTR("com.apple.Accessibility"),kCFPreferencesCurrentUser,kCFPreferencesAnyHost);
        axConfigured=CFPreferencesAppSynchronize(CFSTR("com.apple.Accessibility"));axPreparationMode=@"preferences";
    }
    axBaselineCaptured=YES;axPrepared=YES;
    // Go SDK 已有并发运行时，不能在这里临时切换整个进程的 euid/egid。
    // 宿主应以 mobile 身份启动；环境状态如实返回，采集是否成功仍由真实快照决定。
}

// 属性编号来自所提供样本的缓存与节点构造调用链，不等同于 Apple 的公开稳定协议。
// 所有入口均先检测 selector；系统版本或授权不满足时返回错误，不伪造成功空树。
static int AXLoadRuntime(void) {
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        axEnvironmentLock=[NSRecursiveLock new];
        const char *libraries[] = {
            "/System/Library/PrivateFrameworks/AXRuntime.framework/AXRuntime",
            "/System/Library/PrivateFrameworks/AccessibilityUI.framework/AccessibilityUI",
            "/System/Library/PrivateFrameworks/AccessibilityUIUtilities.framework/AccessibilityUIUtilities",
            "/usr/lib/libAccessibility.dylib"
        };
        for (NSUInteger i = 0; i < sizeof(libraries)/sizeof(libraries[0]); i++)
            dlopen(libraries[i], RTLD_LAZY | RTLD_GLOBAL);
    });
    Class cls = NSClassFromString(@"AXUIElement");
    return cls && [cls respondsToSelector:NSSelectorFromString(@"uiApplicationAtCoordinate:")] ? 0 : -1;
}

int AXBootstrap(void) {
    int ready=AXLoadRuntime();
    [axEnvironmentLock lock];
    @try { if(!axPrepared)AGAXPrepareEnvironment(); }
    @finally { [axEnvironmentLock unlock]; }
    return ready;
}

ag_ios_result AGAXEnvironment(BOOL restore){
    int ready=AXLoadRuntime();
    [axEnvironmentLock lock];
    @try {
        if(restore){
            if(!axPreviousKnown)return AGError(3,@"首次 AX 设置状态未知，不能猜测恢复值");
            if(axSetEnabled)axSetEnabled(axPreviousEnabled);
            else{CFPreferencesSetValue(CFSTR("FullKeyboardAccessEnabled"),axPreviousEnabled?kCFBooleanTrue:kCFBooleanFalse,CFSTR("com.apple.Accessibility"),kCFPreferencesCurrentUser,kCFPreferencesAnyHost);if(!CFPreferencesAppSynchronize(CFSTR("com.apple.Accessibility")))return AGError(3,@"恢复 AX 设置失败");}
            axConfigured=NO;axPrepared=NO;
        }
        else if(!axPrepared)AGAXPrepareEnvironment();
        return AGJSON(@{@"ready":@(ready==0),@"configured":@(axConfigured),@"mode":axPreparationMode ?: @"unavailable",@"uid":@(geteuid()),@"mobileIdentity":@(geteuid()==501),@"previousKnown":@(axPreviousKnown),@"previousEnabled":@(axPreviousEnabled)});
    } @finally { [axEnvironmentLock unlock]; }
}

static NSDictionary *AGAXAttributes(id element, NSArray *attributes) {
    SEL enable = NSSelectorFromString(@"enableCache:");
    SEL update = NSSelectorFromString(@"updateCacheWithAttributes:");
    SEL cached = NSSelectorFromString(@"cachedAttributes");
    if (!element || ![element respondsToSelector:enable] ||
        ![element respondsToSelector:update] || ![element respondsToSelector:cached]) return nil;
    // 样本传入的是 NSNumber，而不是 BOOL；ABI 类型不能因 selector 名称而猜测。
    ((void(*)(id,SEL,id))objc_msgSend)(element,enable,@YES);
    ((void(*)(id,SEL,id))objc_msgSend)(element,update,attributes);
    id result = ((id(*)(id,SEL))objc_msgSend)(element,cached);
    return [result isKindOfClass:[NSDictionary class]] ? result : nil;
}
static id AGAXAttribute(NSDictionary *attrs, int number) {
    id value = attrs[@(number)] ?: attrs[[@(number) stringValue]];
    return value == [NSNull null] ? nil : value;
}
static NSString *AGAXString(id value) {
    return [value isKindOfClass:[NSString class]] ? value :
           [value isKindOfClass:[NSNumber class]] ? [value stringValue] : @"";
}
static CGRect AGAXRect(id value) {
    if ([value isKindOfClass:[NSValue class]] && strcmp([value objCType],@encode(CGRect)) == 0)
        return [value CGRectValue];
    // 私有 AX 矩形包装在样本中通过 description 读取；保留标准 CGRect 字符串回退。
    NSString *description = [value description];
    if (!description.length) return CGRectNull;
    float x=0,y=0,w=0,h=0;
    if (sscanf(description.UTF8String,"%*[^=]= x:%f y:%f w:%f h:%f",&x,&y,&w,&h) == 4)
        return CGRectMake(x,y,w,h);
    if ([description hasPrefix:@"{{"]) return CGRectFromString(description);
    return CGRectNull;
}
static NSString *AGAXKey(NSDictionary *node) {
    return [NSString stringWithFormat:@"%@|%@|%@|%@|%@|%@|%@|%@|%@",
            node[@"bundle"],node[@"identifier"],node[@"text"],node[@"value"],node[@"class"],
            node[@"left"],node[@"top"],node[@"right"],node[@"bottom"]];
}
static NSDictionary *AGAXNode(NSDictionary *attrs, CGRect parent, CGFloat scale, NSInteger index, NSUInteger children) {
    CGRect rect = AGAXRect(AGAXAttribute(attrs,2003));
    if (CGRectIsNull(rect) || CGRectIsInfinite(rect) ||
        !isfinite(rect.origin.x) || !isfinite(rect.origin.y) ||
        !isfinite(rect.size.width) || !isfinite(rect.size.height) ||
        fabs(rect.origin.x)>100000 || fabs(rect.origin.y)>100000 ||
        rect.size.width<=0 || rect.size.height<=0 || rect.size.width>100000 || rect.size.height>100000) return nil;
    NSString *role=AGAXString(AGAXAttribute(attrs,5003)), *cls=AGAXString(AGAXAttribute(attrs,5004));
    NSString *value=AGAXString(AGAXAttribute(attrs,2006));
    NSMutableDictionary *node=[NSMutableDictionary dictionaryWithDictionary:@{
        @"text":AGAXString(AGAXAttribute(attrs,2001)), @"value":value,
        @"identifier":AGAXString(AGAXAttribute(attrs,5019)), @"class":cls, @"role":role,
        @"bundle":AGAXString(AGAXAttribute(attrs,3003)),
        @"left":@(llround(CGRectGetMinX(rect)*scale)), @"top":@(llround(CGRectGetMinY(rect)*scale)),
        @"right":@(llround(CGRectGetMaxX(rect)*scale)), @"bottom":@(llround(CGRectGetMaxY(rect)*scale)),
        @"childCount":@(children), @"index":@(index)
    }];
    if (!CGRectIsNull(parent)) {
        node[@"parentLeft"]=@(llround((rect.origin.x-parent.origin.x)*scale));
        node[@"parentTop"]=@(llround((rect.origin.y-parent.origin.y)*scale));
        node[@"parentRight"]=@(llround((CGRectGetMaxX(rect)-parent.origin.x)*scale));
        node[@"parentBottom"]=@(llround((CGRectGetMaxY(rect)-parent.origin.y)*scale));
    }
    id enabled=AGAXAttribute(attrs,5034), traits=AGAXAttribute(attrs,2004);
    if([enabled respondsToSelector:@selector(boolValue)])node[@"enabled"]=@([enabled boolValue]);
    BOOL roleScroll=[role hasSuffix:@"ScrollView"] || [role hasSuffix:@"TableView"] || [role hasSuffix:@"CollectionView"] || [role hasSuffix:@"WebView"];
    BOOL classScroll=[cls hasSuffix:@"ScrollView"] || [cls hasSuffix:@"TableView"] || [cls hasSuffix:@"CollectionView"] || [cls hasSuffix:@"WebView"];
    node[@"scrollable"]=@(roleScroll || classScroll);
    if([traits respondsToSelector:@selector(unsignedLongLongValue)]){
        // 这些掩码由样本 0x1010bde7c 起的按位判断及 JSON 数组槽位核实，不再按类名猜状态。
        uint64_t bits=[traits unsignedLongLongValue];
        node[@"traits"]=@(bits);node[@"button"]=@((bits&UINT64_C(1))!=0);
        node[@"clickable"]=@((bits&UINT64_C(1))!=0);node[@"selected"]=@((bits&UINT64_C(8))!=0);
        BOOL disabled=(bits&UINT64_C(0x100))!=0;node[@"disabled"]=@(disabled);if(disabled)node[@"enabled"]=@NO;
        node[@"header"]=@((bits&UINT64_C(0x10000))!=0);node[@"adjustable"]=@((bits&UINT64_C(0x1000))!=0);
        node[@"textEntry"]=@((bits&UINT64_C(0x40000))!=0);node[@"editable"]=node[@"textEntry"];
        node[@"statusBar"]=@((bits&UINT64_C(0x800000))!=0);node[@"password"]=@((bits&UINT64_C(0x1000000))!=0);
        BOOL toggle=(bits&UINT64_C(0x2000000))!=0;node[@"toggle"]=@(toggle);
        BOOL switchLike=toggle || (bits&(UINT64_C(1)<<53)) || [role containsString:@"Switch"] || [cls containsString:@"Switch"];
        node[@"checked"]=@(switchLike && ([value isEqualToString:@"1"] || [value caseInsensitiveCompare:@"on"]==NSOrderedSame));
    }
    return node;
}
static void AGAXAppend(NSDictionary *node, NSMutableArray *nodes, NSMutableSet *seen, NSUInteger limit) {
    if (!node || nodes.count>=limit) return;
    NSString *key=AGAXKey(node);
    if (![seen containsObject:key]) { [seen addObject:key]; [nodes addObject:node]; }
}

NSArray *AGAXCollectNodes(NSUInteger limit, NSString **error) {
    if (AXBootstrap()!=0) { if(error)*error=@"AXUIElement 私有采集入口不可用"; return nil; }
    // 屏幕信息先经已有主线程边界读取；不能在持有 AX 锁时 dispatch_sync 等待主线程。
    ag_ios_result display=AGOnMain(^{
        CGRect bounds=UIScreen.mainScreen.bounds;
        return AGJSON(@{@"x":@(bounds.origin.x),@"y":@(bounds.origin.y),@"w":@(bounds.size.width),@"h":@(bounds.size.height),@"scale":@(UIScreen.mainScreen.scale)});
    });
    if(display.code){if(error)*error=display.data ? [[[NSString alloc] initWithBytes:display.data length:display.length encoding:NSUTF8StringEncoding] autorelease] : @"主线程屏幕读取失败";free(display.data);return nil;}
    NSDictionary *geometry=[NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:display.data length:display.length] options:0 error:NULL];free(display.data);
    CGRect screen=CGRectMake([geometry[@"x"] doubleValue],[geometry[@"y"] doubleValue],[geometry[@"w"] doubleValue],[geometry[@"h"] doubleValue]);
    CGFloat scale=[geometry[@"scale"] doubleValue];
    if(CGRectIsEmpty(screen) || scale<=0){if(error)*error=@"无法取得屏幕几何信息";return nil;}
    // C ABI 与 Go 入口共用同一采集器，避免同时改变系统对象缓存。
    @synchronized(NSClassFromString(@"AXUIElement")) {
        @try {
            Class cls=NSClassFromString(@"AXUIElement");
            NSMutableDictionary *applications=[NSMutableDictionary dictionary];
            // 取样用于发现前台 App、键盘和系统弹窗，不限于一个进程的根节点。
            for (int row=0;row<3;row++) for(int col=0;col<3;col++) {
                CGPoint point=CGPointMake(screen.origin.x+screen.size.width*(col+0.5)/3,screen.origin.y+screen.size.height*(row+0.5)/3);
                id app=((id(*)(id,SEL,CGPoint))objc_msgSend)(cls,NSSelectorFromString(@"uiApplicationAtCoordinate:"),point);
                SEL pid=NSSelectorFromString(@"pid");
                if ([app respondsToSelector:pid]) {
                    int identifier=((int(*)(id,SEL))objc_msgSend)(app,pid);
                    if(identifier>0) applications[@(identifier)]=app;
                }
            }
            if(!applications.count){if(error)*error=@"AX 未取得应用：请检查设备的 Accessibility 环境与授权";return nil;}
            NSMutableArray *nodes=[NSMutableArray array]; NSMutableSet *seen=[NSMutableSet set];
            for (NSNumber *pid in [[applications allKeys] sortedArrayUsingSelector:@selector(compare:)]) {
                NSDictionary *cached=AGAXAttributes(applications[pid],@[@5039]);
                id root=AGAXAttribute(cached,5039);
                NSMutableArray *queue=[NSMutableArray array];
                if([root isKindOfClass:[NSDictionary class]]) [queue addObject:@{@"tree":root,@"parent":[NSValue valueWithCGRect:CGRectNull],@"index":@0,@"depth":@0}];
                else if([root isKindOfClass:[NSArray class]]) for(id tree in root) if([tree isKindOfClass:[NSDictionary class]]) [queue addObject:@{@"tree":tree,@"parent":[NSValue valueWithCGRect:CGRectNull],@"index":@0,@"depth":@0}];
                NSHashTable *visited=[NSHashTable hashTableWithOptions:NSPointerFunctionsObjectPointerPersonality];
                for(NSUInteger head=0;head<queue.count && head<limit && nodes.count<limit;head++) {
                    NSDictionary *item=queue[head], *tree=item[@"tree"];
                    if([visited containsObject:tree])continue;[visited addObject:tree];
                    NSDictionary *attrs=tree[@"UIAccessibilitySnapshotKeyAttributes"];
                    if(![attrs isKindOfClass:[NSDictionary class]])attrs=@{};
                    NSArray *children=tree[@"UIAccessibilitySnapshotKeyChildren"];
                    if(![children isKindOfClass:[NSArray class]])children=@[];
                    CGRect rect=AGAXRect(AGAXAttribute(attrs,2003));
                    AGAXAppend(AGAXNode(attrs,[item[@"parent"] CGRectValue],scale,[item[@"index"] integerValue],children.count),nodes,seen,limit);
                    NSUInteger depth=[item[@"depth"] unsignedIntegerValue];
                    if(depth>=64)continue;
                    for(NSUInteger i=0;i<children.count && queue.count<limit;i++) if([children[i] isKindOfClass:[NSDictionary class]])
                        [queue addObject:@{@"tree":children[i],@"parent":[NSValue valueWithCGRect:rect],@"index":@(i),@"depth":@(depth+1)}];
                }
            }
            // 快照覆盖不到的可见元素，通过有界坐标网格补查；不猜测它们的父节点与兄弟索引。
            SEL hit=NSSelectorFromString(@"uiElementAtCoordinate:");
            if([cls respondsToSelector:hit]) for(int row=0;row<12 && nodes.count<limit;row++) for(int col=0;col<6 && nodes.count<limit;col++) {
                CGPoint point=CGPointMake(screen.origin.x+screen.size.width*(col+0.5)/6,screen.origin.y+screen.size.height*(row+0.5)/12);
                id el=((id(*)(id,SEL,CGPoint))objc_msgSend)(cls,hit,point);
                NSDictionary *attrs=AGAXAttributes(el,@[@2003,@2001,@2006,@5003,@5004,@3003,@5019,@2004,@5034]);
                if(attrs) AGAXAppend(AGAXNode(attrs,CGRectNull,scale,-1,0),nodes,seen,limit);
            }
            if(!nodes.count){if(error)*error=@"AX 未产生有效控件快照：接口存在不代表跨应用采集已获授权";return nil;}
            [nodes sortUsingComparator:^NSComparisonResult(NSDictionary *a,NSDictionary *b){
                for(NSString *key in @[@"top",@"left",@"bundle",@"identifier",@"text",@"value",@"right",@"bottom",@"class"]){NSComparisonResult r=[a[key] compare:b[key]];if(r!=NSOrderedSame)return r;}return NSOrderedSame;
            }];
            return nodes;
        } @catch(NSException *exception) {
            if(error)*error=[NSString stringWithFormat:@"AX 采集异常: %@",exception.reason];return nil;
        }
    }
}
