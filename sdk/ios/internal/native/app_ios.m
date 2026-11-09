//go:build ios && cgo

#import "runtime.h"

ag_ios_result AGApplication(NSString *op, NSDictionary *args) {
    if ([op isEqualToString:@"app.foreground"]) {
        CFStringRef (*copyFront)(void) = AGSymbol("SBSCopyFrontmostApplicationDisplayIdentifier");
        if (!copyFront) return AGError(1,@"前台应用接口不可用");
        CFStringRef identifier = copyFront();
        if (!identifier) return AGError(3,@"无法读取前台应用，需检查系统授权");
        ag_ios_result r = AGJSON(@{@"bundleId":(NSString *)identifier});
        CFRelease(identifier);
        return r;
    }
    return AGOnMain(^{
        Class cls = NSClassFromString(@"LSApplicationWorkspace");
        SEL selector = NSSelectorFromString(@"defaultWorkspace");
        if (![cls respondsToSelector:selector]) return AGError(1,@"LSApplicationWorkspace 不可用");
        id workspace = ((id(*)(id,SEL))objc_msgSend)(cls,selector);
        if ([op isEqualToString:@"app.launch"]) {
            SEL launch = NSSelectorFromString(@"openApplicationWithBundleID:");
            if (![workspace respondsToSelector:launch]) return AGError(1,@"启动应用接口不可用");
            BOOL accepted = ((BOOL(*)(id,SEL,id))objc_msgSend)(workspace,launch,args[@"bundleId"]);
            if (!accepted) return AGError(3,@"系统拒绝启动应用，可能未安装或缺少权限");
            return AGJSON(@{@"submitted":@YES});
        }
        if ([op isEqualToString:@"app.list"]) {
            SEL list = NSSelectorFromString(@"allApplications");
            if (![workspace respondsToSelector:list]) return AGError(1,@"应用列表接口不可用");
            id apps = ((id(*)(id,SEL))objc_msgSend)(workspace,list);
            if (![apps isKindOfClass:[NSArray class]]) return AGError(3,@"应用列表响应无效");
            NSMutableArray *result = [NSMutableArray array];
            for (id app in apps) {
                SEL identifier = NSSelectorFromString(@"bundleIdentifier");
                if (![app respondsToSelector:identifier]) identifier = NSSelectorFromString(@"applicationIdentifier");
                if (![app respondsToSelector:identifier]) continue;
                NSString *bundleID = ((id(*)(id,SEL))objc_msgSend)(app,identifier);
                SEL label = NSSelectorFromString(@"localizedName"), version = NSSelectorFromString(@"shortVersionString");
                id name = [app respondsToSelector:label] ? ((id(*)(id,SEL))objc_msgSend)(app,label) : @"";
                id ver = [app respondsToSelector:version] ? ((id(*)(id,SEL))objc_msgSend)(app,version) : @"";
                if (bundleID) [result addObject:@{@"bundleId":bundleID,@"name":name ?: @"",@"version":ver ?: @""}];
                if (result.count >= 10000) return AGError(3,@"应用列表超过限制");
            }
            return AGJSON(result);
        }
        return AGError(1,@"未知应用操作");
    });
}
