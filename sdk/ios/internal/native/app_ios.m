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

static ag_ios_result AGApplicationBody(NSString *op, NSDictionary *args) {
    NSString *bundleID = args[@"bundleId"];
    // 安装工具的进程等待必须在 UIKit 主线程外；未命中 helper 时再走 workspace。
    if([op isEqualToString:@"app.install"]){BOOL handled=NO;ag_ios_result result=AGInstallPackage(args[@"path"],&handled);if(handled || result.code)return result;free(result.data);}
    if([op isEqualToString:@"app.uninstall"]){BOOL handled=NO;ag_ios_result result=AGUninstallPackage(bundleID,&handled);if(handled || result.code)return result;free(result.data);}

    if ([op isEqualToString:@"app.foreground"]) {
        return AGOnMain(^{
            CFStringRef (*copyFront)(void)=AGSymbol("SBSCopyFrontmostApplicationDisplayIdentifier");
            CFStringRef identifier=copyFront ? copyFront() : NULL;
            if(identifier){ag_ios_result result=AGJSON(@{@"bundleId":(NSString *)identifier,@"source":@"SpringBoardServices"});CFRelease(identifier);return result;}
            // 只有实际存在前台活动 Scene 时，才能返回本进程真实包名。
            // 独立 worker 没有 Scene，不得把宿主包名冒充当前前台应用。
            for(UIScene *scene in UIApplication.sharedApplication.connectedScenes){
                if(scene.activationState==UISceneActivationStateForegroundActive){
                    NSString *own=NSBundle.mainBundle.bundleIdentifier;
                    if(own.length)return AGJSON(@{@"bundleId":own,@"source":@"activeScene"});
                }
            }
            return AGAXForegroundApplication();
        });
    }

    // app 包路径：给"执行 app 包内附带的可执行文件"用（例如 frpc-ios）。
    // 用 NSBundle 而不是猜路径：iOS 上包路径带 UUID，猜必错。
    if ([op isEqualToString:@"app.bundle_dir"]) {
        NSString *dir = AGApplicationBundlePath();
        if (dir.length == 0) return AGError(3,@"拿不到 app 包路径");
        return AGJSON(@{@"path":dir});
    }

    // 自身包名不需要任何私有入口。
    if ([op isEqualToString:@"app.self"]) {
        NSString *selfID = [NSBundle bundleWithPath:AGApplicationBundlePath()].bundleIdentifier;
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
            Class serviceClass=NSClassFromString(@"FBSSystemService");SEL shared=NSSelectorFromString(@"sharedService");
            id service=[serviceClass respondsToSelector:shared]?((id(*)(id,SEL))objc_msgSend)(serviceClass,shared):nil;
            SEL kill=NSSelectorFromString(@"terminateApplication:forReason:andReport:withDescription:");
            if([service respondsToSelector:kill]){
                // 样本传入 bundleID 与 reason=5，不能把 pid 按对象 ABI 传入。
                ((void(*)(id,SEL,id,NSInteger,BOOL,id))objc_msgSend)(service,kill,bundleID,5,NO,nil);
                return AGJSON(@{@"submitted":@YES,@"stopped":@YES});
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
            NSData *png=nil;
            // 图标可能仅在 Assets.car 中；不能只依赖某个版本的 proxy 选择器。
            SEL imageSelector=NSSelectorFromString(@"_applicationIconImageForBundleIdentifier:format:scale:");
            @try {
                if([UIImage respondsToSelector:imageSelector]){
                    id image=((id(*)(id,SEL,id,NSInteger,CGFloat))objc_msgSend)(UIImage.class,imageSelector,bundleID,2,UIScreen.mainScreen.scale);
                    if([image isKindOfClass:UIImage.class])png=UIImagePNGRepresentation(image);
                }
            }@catch(NSException *exception){ /* 当前系统不接受该私有入口，继续读取真实资源。 */ }
            if(!png){
                SEL iconSelector=NSSelectorFromString(@"iconForVariant:");
                @try {
                    if([proxy respondsToSelector:iconSelector]){
                        id image=((id(*)(id,SEL,NSUInteger))objc_msgSend)(proxy,iconSelector,0);
                        if([image isKindOfClass:UIImage.class])png=UIImagePNGRepresentation(image);
                    }
                }@catch(NSException *exception){ /* 继续包资源回退。 */ }
            }
            if(!png){
                NSURL *url=AGRead(proxy,@"bundleURL",nil);
                NSBundle *bundle=[url isKindOfClass:NSURL.class] ? [NSBundle bundleWithURL:url] : nil;
                if([bundleID isEqualToString:[NSBundle bundleWithPath:AGApplicationBundlePath()].bundleIdentifier])bundle=[NSBundle bundleWithPath:AGApplicationBundlePath()];
                NSDictionary *info=bundle.infoDictionary;
                NSArray *names=info[@"CFBundleIcons"][@"CFBundlePrimaryIcon"][@"CFBundleIconFiles"] ?: info[@"CFBundleIconFiles"];
                if([names isKindOfClass:NSArray.class])for(id name in [names reverseObjectEnumerator]){
                    if(![name isKindOfClass:NSString.class])continue;
                    UIImage *image=[UIImage imageNamed:name inBundle:bundle compatibleWithTraitCollection:nil];
                    if(image){png=UIImagePNGRepresentation(image);if(png)break;}
                }
            }
            if(!png)return AGError(3,[NSString stringWithFormat:@"应用 %@ 的系统图标入口及包资源均不可读",bundleID]);
            return AGData(png);
        }

        // 安装/卸载/清数据：必须走 MobileInstallation（私有服务），
        // 且需要 platform-application 权限；没有权限时明确报不可用。
        if ([op isEqualToString:@"app.install"]) {
            NSString *path = args[@"path"];
            if (path.length == 0) return AGError(2,@"缺少 path");
            NSString *resolved=[[path stringByStandardizingPath] stringByResolvingSymlinksInPath];
            if(![[NSFileManager defaultManager] fileExistsAtPath:resolved])return AGError(2,@"安装包不存在");
            SEL install=NSSelectorFromString(@"installApplication:withOptions:error:");
            if(![workspace respondsToSelector:install])return AGError(1,@"LSApplicationWorkspace 安装入口不可用，需要受支持的已授权安装环境");
            NSError *error=nil;BOOL accepted=((BOOL(*)(id,SEL,id,id,NSError **))objc_msgSend)(workspace,install,[NSURL fileURLWithPath:resolved],@{},&error);
            return accepted?AGJSON(@{@"installed":@YES}):AGError(3,error.localizedDescription ?: @"系统拒绝安装");
        }
        if([op isEqualToString:@"app.uninstall"]){
            if(!bundleID.length)return AGError(2,@"缺少 bundleId");
            SEL uninstall=NSSelectorFromString(@"uninstallApplication:withOptions:");
            if(![workspace respondsToSelector:uninstall])return AGError(1,@"LSApplicationWorkspace 卸载入口不可用");
            BOOL accepted=((BOOL(*)(id,SEL,id,id))objc_msgSend)(workspace,uninstall,bundleID,@{});
            return accepted?AGJSON(@{@"uninstalled":@YES}):AGError(3,@"系统拒绝卸载");
        }
        if([op isEqualToString:@"app.clear"]){
            if(!bundleID.length || [bundleID isEqualToString:[NSBundle bundleWithPath:AGApplicationBundlePath()].bundleIdentifier])return AGError(2,@"包名为空或正在清理 SDK 宿主自身");
            id url=AGRead(AGProxy(bundleID),@"dataContainerURL",nil);
            if(![url isKindOfClass:[NSURL class]])return AGError(3,@"应用数据容器不可读");
            NSString *root=[[[url path] stringByStandardizingPath] stringByResolvingSymlinksInPath];
            if(![root hasPrefix:@"/private/var/mobile/Containers/Data/Application/"] && ![root hasPrefix:@"/var/mobile/Containers/Data/Application/"])return AGError(2,@"拒绝清理不属于应用数据容器的路径");
            if([[[NSUUID alloc] initWithUUIDString:root.lastPathComponent] autorelease]==nil)return AGError(2,@"容器标识无效");
            NSError *error=nil;NSFileManager *manager=NSFileManager.defaultManager;NSArray *children=[manager contentsOfDirectoryAtPath:root error:&error];if(!children)return AGError(3,error.localizedDescription);
            // 参考样本逐项删除容器内容；保留容器管理元数据，不能删除容器根或跟随链接递归到外部。
            for(NSString *name in children){if([name hasPrefix:@".com.apple."])continue;NSString *child=[root stringByAppendingPathComponent:name];if(![manager removeItemAtPath:child error:&error])return AGError(4,error.localizedDescription ?: @"部分数据已清理，请核对");}
            return AGJSON(@{@"cleared":@YES});
        }

        return AGError(1,@"未知应用操作");
    });
}

ag_ios_result AGApplication(NSString *op,NSDictionary *args){
    if(AGInputWriteRequired(op))return AGWithInputLease(^{return AGApplicationBody(op,args);});
    return AGApplicationBody(op,args);
}
