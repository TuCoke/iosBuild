//go:build ios && cgo

// 应用管理原生实现：启动/强杀/包信息/路径/图标/安装/卸载/清数据/打开 URL。
//
// 所有私有入口都先 dlsym 再用 respondsToSelector 判定：符号不在或对象不认这个选择器时
// 明确返回"不可用"，而不是假装成功 —— 这些接口在不同 iOS 版本上可用性差异很大。
#import "runtime.h"

// AGRead 读取对象上某个无参选择器的返回值；不支持时返回 fallback。
static id AGRead(id object, NSString *selectorName, id fallback) {
    SEL selector = NSSelectorFromString(selectorName);
    if (!object || ![object respondsToSelector:selector]) return fallback;
    return ((id(*)(id,SEL))objc_msgSend)(object, selector);
}

// AGProxy 取 LSApplicationProxy（某个 bundle 的应用描述对象）。
static id AGProxy(NSString *bundleID) {
    Class proxyClass = NSClassFromString(@"LSApplicationProxy");
    SEL forBundle = NSSelectorFromString(@"applicationProxyForIdentifier:");
    if ([proxyClass respondsToSelector:forBundle]) {
        return ((id(*)(id,SEL,id))objc_msgSend)(proxyClass, forBundle, bundleID);
    }
    return nil;
}

ag_ios_result AGApplication(NSString *op, NSDictionary *args) {
    NSString *bundleID = args[@"bundleId"];

    if ([op isEqualToString:@"app.foreground"]) {
        CFStringRef (*copyFront)(void) = AGSymbol("SBSCopyFrontmostApplicationDisplayIdentifier");
        if (!copyFront) return AGError(1,@"前台应用接口不可用");
        CFStringRef identifier = copyFront();
        if (!identifier) return AGError(3,@"无法读取前台应用，需检查系统授权");
        ag_ios_result r = AGJSON(@{@"bundleId":(NSString *)identifier});
        CFRelease(identifier);
        return r;
    }

    // 自身包名不需要任何私有入口。
    if ([op isEqualToString:@"app.self"]) {
        NSString *selfID = [[NSBundle mainBundle] bundleIdentifier];
        if (!selfID) return AGError(3,@"无法读取自身 bundleId");
        return AGJSON(@{@"bundleId":selfID});
    }

    return AGOnMain(^{
        Class cls = NSClassFromString(@"LSApplicationWorkspace");
        SEL selector = NSSelectorFromString(@"defaultWorkspace");
        if (![cls respondsToSelector:selector]) return AGError(1,@"LSApplicationWorkspace 不可用");
        id workspace = ((id(*)(id,SEL))objc_msgSend)(cls,selector);

        if ([op isEqualToString:@"app.launch"]) {
            if (bundleID.length == 0) return AGError(2,@"缺少 bundleId");
            SEL launch = NSSelectorFromString(@"openApplicationWithBundleID:");
            if (![workspace respondsToSelector:launch]) return AGError(1,@"启动应用接口不可用");
            BOOL accepted = ((BOOL(*)(id,SEL,id))objc_msgSend)(workspace,launch,bundleID);
            if (!accepted) return AGError(3,@"系统拒绝启动应用，可能未安装或缺少权限");
            return AGJSON(@{@"submitted":@YES});
        }

        // 打开 URL / Scheme：先试 openURL:，部分系统版本只认 openSensitiveURL:withOptions:。
        if ([op isEqualToString:@"app.open_url"]) {
            NSString *url = args[@"url"];
            if (url.length == 0) return AGError(2,@"缺少 url");
            NSURL *target = [NSURL URLWithString:url];
            if (!target) return AGError(2,@"url 无效");
            SEL simple = NSSelectorFromString(@"openURL:");
            if ([workspace respondsToSelector:simple]) {
                BOOL ok = ((BOOL(*)(id,SEL,id))objc_msgSend)(workspace, simple, target);
                return ok ? AGJSON(@{@"opened":@YES}) : AGError(3,@"系统拒绝打开该 URL");
            }
            SEL sensitive = NSSelectorFromString(@"openSensitiveURL:withOptions:");
            if ([workspace respondsToSelector:sensitive]) {
                BOOL ok = ((BOOL(*)(id,SEL,id,id))objc_msgSend)(workspace, sensitive, target, @{});
                return ok ? AGJSON(@{@"opened":@YES}) : AGError(3,@"系统拒绝打开该 URL");
            }
            return AGError(1,@"打开 URL 接口不可用");
        }

        // 强杀：先试 workspace 的 terminate（带 reason/source），再退到 FBSSystemService。
        if ([op isEqualToString:@"app.stop"]) {
            if (bundleID.length == 0) return AGError(2,@"缺少 bundleId");
            SEL terminate = NSSelectorFromString(@"terminateApplicationForBundleIdentifier:withReason:withSource:");
            if ([workspace respondsToSelector:terminate]) {
                ((void(*)(id,SEL,id,NSUInteger,NSUInteger))objc_msgSend)(workspace, terminate, bundleID, 0, 0);
                return AGJSON(@{@"stopped":@YES});
            }
            id service = ((id(*)(id,SEL))objc_msgSend)(NSClassFromString(@"FBSSystemService"),
                                                      NSSelectorFromString(@"sharedService"));
            SEL kill = NSSelectorFromString(@"terminateApplication:forReason:andReport:withDescription:");
            if (service && [service respondsToSelector:kill]) {
                pid_t pid = 0;
                SEL findByBundle = NSSelectorFromString(@"processIdentifierForBundleIdentifier:");
                if ([service respondsToSelector:findByBundle]) {
                    pid = ((pid_t(*)(id,SEL,id))objc_msgSend)(service, findByBundle, bundleID);
                }
                if (pid > 0) {
                    ((void(*)(id,SEL,pid_t,NSUInteger,BOOL,id))objc_msgSend)(service, kill, pid, 0, NO, nil);
                    return AGJSON(@{@"stopped":@YES});
                }
            }
            return AGError(1,@"强杀接口不可用");
        }

        if ([op isEqualToString:@"app.list"] || [op isEqualToString:@"app.list_all"]) {
            BOOL includeSystem = [op isEqualToString:@"app.list_all"] ? [args[@"system"] boolValue] : NO;
            SEL list = NSSelectorFromString(@"allApplications");
            if (![workspace respondsToSelector:list]) return AGError(1,@"应用列表接口不可用");
            id apps = ((id(*)(id,SEL))objc_msgSend)(workspace,list);
            if (![apps isKindOfClass:[NSArray class]]) return AGError(3,@"应用列表响应无效");
            NSMutableArray *result = [NSMutableArray array];
            for (id app in apps) {
                SEL identifier = NSSelectorFromString(@"bundleIdentifier");
                if (![app respondsToSelector:identifier]) identifier = NSSelectorFromString(@"applicationIdentifier");
                if (![app respondsToSelector:identifier]) continue;
                NSString *appID = ((id(*)(id,SEL))objc_msgSend)(app,identifier);
                if (!appID) continue;
                SEL systemFlag = NSSelectorFromString(@"isSystemApplication");
                BOOL isSystem = [app respondsToSelector:systemFlag] &&
                                ((BOOL(*)(id,SEL))objc_msgSend)(app, systemFlag);
                if (isSystem && !includeSystem) continue;
                id name = AGRead(app, @"localizedName", @"");
                id version = AGRead(app, @"shortVersionString", @"");
                id build = AGRead(app, @"bundleVersion", @"");
                [result addObject:@{@"bundleId":appID,@"name":name ?: @"",@"version":version ?: @"",
                                    @"build":build ?: @"",@"system":@(isSystem)}];
                if (result.count >= 10000) return AGError(3,@"应用列表超过限制");
            }
            return AGJSON(result);
        }

        if ([op isEqualToString:@"app.info"]) {
            if (bundleID.length == 0) return AGError(2,@"缺少 bundleId");
            id proxy = AGProxy(bundleID);
            if (!proxy) return AGError(3,@"应用未安装或应用信息不可读");
            return AGJSON(@{
                @"name": AGRead(proxy, @"localizedName", @"") ?: @"",
                @"version": AGRead(proxy, @"shortVersionString", @"") ?: @"",
                @"build": AGRead(proxy, @"bundleVersion", @"") ?: @"",
            });
        }

        if ([op isEqualToString:@"app.installed"]) {
            if (bundleID.length == 0) return AGError(2,@"缺少 bundleId");
            return AGJSON(@{@"installed":@((BOOL)(AGProxy(bundleID) != nil))});
        }

        if ([op isEqualToString:@"app.bundle_path"]) {
            if (bundleID.length == 0) return AGError(2,@"缺少 bundleId");
            id url = AGRead(AGProxy(bundleID), @"bundleURL", nil);
            return AGJSON(@{@"path": [url isKindOfClass:[NSURL class]] ? [url path] : @""});
        }

        if ([op isEqualToString:@"app.data_path"]) {
            if (bundleID.length == 0) return AGError(2,@"缺少 bundleId");
            id url = AGRead(AGProxy(bundleID), @"dataContainerURL", nil);
            return AGJSON(@{@"path": [url isKindOfClass:[NSURL class]] ? [url path] : @""});
        }

        if ([op isEqualToString:@"app.icon"]) {
            if (bundleID.length == 0) return AGError(2,@"缺少 bundleId");
            id proxy = AGProxy(bundleID);
            if (!proxy) return AGError(3,@"应用未安装");
            NSData *png = nil;
            SEL iconSelector = NSSelectorFromString(@"iconForVariant:");
            if ([proxy respondsToSelector:iconSelector]) {
                id image = ((id(*)(id,SEL,NSUInteger))objc_msgSend)(proxy, iconSelector, 0);
                if ([image isKindOfClass:[UIImage class]]) png = UIImagePNGRepresentation(image);
            }
            if (!png) return AGError(3,@"图标不可读");
            return AGData(png);
        }

        // 安装/卸载/清数据：必须走 MobileInstallation（私有服务），
        // 且需要 platform-application 权限；没有权限时明确报不可用。
        if ([op isEqualToString:@"app.install"]) {
            NSString *path = args[@"path"];
            if (path.length == 0) return AGError(2,@"缺少 path");
            id mobile = NSClassFromString(@"MobileInstallation");
            SEL install = NSSelectorFromString(@"installApplication:withOptions:error:");
            if (![mobile respondsToSelector:install]) return AGError(1,@"安装接口不可用（需要 platform-application 权限）");
            NSError *err = nil;
            BOOL ok = ((BOOL(*)(id,SEL,id,id,id*))objc_msgSend)(mobile, install, path, @{}, &err);
            return ok ? AGJSON(@{@"installed":@YES}) : AGError(3, err.localizedDescription ?: @"安装失败");
        }
        if ([op isEqualToString:@"app.uninstall"]) {
            if (bundleID.length == 0) return AGError(2,@"缺少 bundleId");
            id mobile = NSClassFromString(@"MobileInstallation");
            SEL uninstall = NSSelectorFromString(@"uninstallApplication:withOptions:error:");
            if (![mobile respondsToSelector:uninstall]) return AGError(1,@"卸载接口不可用（需要 platform-application 权限）");
            NSError *err = nil;
            BOOL ok = ((BOOL(*)(id,SEL,id,id,id*))objc_msgSend)(mobile, uninstall, bundleID, @{}, &err);
            return ok ? AGJSON(@{@"uninstalled":@YES}) : AGError(3, err.localizedDescription ?: @"卸载失败");
        }
        if ([op isEqualToString:@"app.clear"]) {
            if (bundleID.length == 0) return AGError(2,@"缺少 bundleId");
            id mobile = NSClassFromString(@"MobileInstallation");
            SEL clear = NSSelectorFromString(@"clearApplicationData:completion:");
            if (![mobile respondsToSelector:clear]) return AGError(1,@"清数据接口不可用（需要 platform-application 权限）");
            __block BOOL done = NO;
            ((void(*)(id,SEL,id,id))objc_msgSend)(mobile, clear, bundleID, ^(BOOL ok){ done = ok; });
            return done ? AGJSON(@{@"cleared":@YES}) : AGError(3,@"清数据失败");
        }

        return AGError(1,@"未知应用操作");
    });
}
