// AGSDKLoader.m
#import "AGSDKLoader.h"
#import <dlfcn.h>
#import <CommonCrypto/CommonDigest.h>

@implementation AGSDKLoader {
    void *_handle;
    AGInvokeFn _invoke;
    AGFreeFn _free;
}

@synthesize loaded = _loaded;
@synthesize path = _path;
@synthesize loadError = _loadError;
@synthesize lastError = _lastError;

+ (instancetype)shared {
    static AGSDKLoader *shared = nil;
    static dispatch_once_t once;
    dispatch_once(&once, ^{ shared = [[AGSDKLoader alloc] init]; });
    return shared;
}

// 与 Go update.ReleaseState 共享同一状态文件；只保存摘要，绝不信任外部传入的路径。
- (NSString *)pluginsDirectory {
    NSString *docs = NSSearchPathForDirectoriesInDomains(NSDocumentDirectory, NSUserDomainMask, YES).firstObject;
    return docs.length ? [docs stringByAppendingPathComponent:@"Plugins"] : nil;
}
- (BOOL)validDigest:(id)value {
    if (![value isKindOfClass:NSString.class] || [value length] != 64) return NO;
    return [value rangeOfCharacterFromSet:[[NSCharacterSet characterSetWithCharactersInString:@"0123456789abcdef"] invertedSet]].location == NSNotFound;
}
- (NSMutableDictionary *)releaseState {
    NSString *dir = [self pluginsDirectory];
    NSData *data = dir ? [NSData dataWithContentsOfFile:[dir stringByAppendingPathComponent:@"release.json"]] : nil;
    id decoded = data ? [NSJSONSerialization JSONObjectWithData:data options:NSJSONReadingMutableContainers error:nil] : nil;
    if (![decoded isKindOfClass:NSMutableDictionary.class] || ![decoded[@"version"] isKindOfClass:NSNumber.class] || [decoded[@"version"] integerValue] != 1) return [@{@"version":@1} mutableCopy];
    for (NSString *key in @[@"current",@"previous",@"pending",@"attempt"]) {
        if (decoded[key] && ![decoded[key] isEqual:@""] && ![self validDigest:decoded[key]]) return [@{@"version":@1,@"lastError":@"版本状态摘要无效，使用包内 SDK"} mutableCopy];
    }
    return decoded;
}
- (BOOL)saveReleaseState:(NSMutableDictionary *)state {
    NSString *dir = [self pluginsDirectory]; if (!dir) return NO;
    NSError *error = nil;
    if (![NSFileManager.defaultManager createDirectoryAtPath:dir withIntermediateDirectories:YES attributes:@{NSFilePosixPermissions:@0700} error:&error]) return NO;
    state[@"version"] = @1; state[@"updatedAt"] = [NSISO8601DateFormatter.new stringFromDate:NSDate.date];
    NSData *data = [NSJSONSerialization dataWithJSONObject:state options:NSJSONWritingPrettyPrinted error:&error];
    return data && [data writeToFile:[dir stringByAppendingPathComponent:@"release.json"] options:NSDataWritingAtomic error:&error];
}
- (NSString *)pathForDigest:(NSString *)digest {
    if (![self validDigest:digest]) return nil;
    NSString *path = [[self pluginsDirectory] stringByAppendingPathComponent:[NSString stringWithFormat:@"versions/%@/libagovios.dylib",digest]];
    // 启动前重新验证，防止路径中的文件在安装后被替换。
    NSDictionary *attrs = [NSFileManager.defaultManager attributesOfItemAtPath:path error:nil];
    if (![attrs[NSFileType] isEqual:NSFileTypeRegular] || [attrs fileSize] < 1024 || [attrs fileSize] > 128*1024*1024) return nil;
    NSData *data = [NSData dataWithContentsOfFile:path options:NSDataReadingMappedIfSafe error:nil];
    if (!data) return nil;
    unsigned char hash[CC_SHA256_DIGEST_LENGTH]; CC_SHA256(data.bytes,(CC_LONG)data.length,hash);
    NSMutableString *actual = [NSMutableString string]; for (int i=0;i<CC_SHA256_DIGEST_LENGTH;i++) [actual appendFormat:@"%02x",hash[i]];
    return [actual isEqualToString:digest] ? path : nil;
}
- (NSArray<NSString *> *)candidatePaths {
    NSMutableArray *paths = [NSMutableArray array];
    NSMutableDictionary *state = [self releaseState];
    if ([state[@"attempt"] length]) {
        state[@"lastError"] = @"候选 SDK 在健康确认前退出，已回退";
        [state removeObjectForKey:@"pending"]; [state removeObjectForKey:@"attempt"];
        if (![self saveReleaseState:state]) return @[];
    }
    if([state[@"current"] length] && ![self pathForDigest:state[@"current"]]) {
        state[@"lastError"]=@"已确认版本文件缺失或摘要不符，回退到上一版本或包内版本";
        if([self pathForDigest:state[@"previous"]])state[@"current"]=state[@"previous"];else [state removeObjectForKey:@"current"];
        [state removeObjectForKey:@"previous"];if(![self saveReleaseState:state])return @[];
    }
    for (NSString *key in @[@"pending",@"current",@"previous"]) {
        NSString *digest = state[key], *path = [self pathForDigest:digest];
        if (path && ![paths containsObject:path]) [paths addObject:path];
        else if ([key isEqualToString:@"pending"] && digest.length) {
            state[@"lastError"] = @"候选 SDK 文件缺失或摘要不符";
            [state removeObjectForKey:@"pending"]; if (![self saveReleaseState:state]) return @[];
        }
    }
    NSString *bundle = NSBundle.mainBundle.bundlePath;
    [paths addObject:[bundle stringByAppendingPathComponent:@"libagovios.dylib"]];
    [paths addObject:[bundle stringByAppendingPathComponent:@"Frameworks/libagovios.dylib"]];
    return paths;
}
// 健康确认是运行时协议探测；受权限限制的截图/AX 不作为升级成败标准。
- (void)confirmCandidate:(NSString *)digest {
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 10*NSEC_PER_SEC),dispatch_get_global_queue(QOS_CLASS_UTILITY,0),^{
        char *raw = self->_invoke("sdk.health","{}");
        NSData *data = raw ? [[NSString stringWithUTF8String:raw] dataUsingEncoding:NSUTF8StringEncoding] : nil;
        if (raw) self->_free(raw);
        id reply = data ? [NSJSONSerialization JSONObjectWithData:data options:0 error:nil] : nil;
        BOOL healthy = [reply isKindOfClass:NSDictionary.class] && [reply[@"ok"] boolValue] && [reply[@"data"] isKindOfClass:NSDictionary.class] && [reply[@"data"][@"ready"] boolValue];
        @synchronized(self) {
            NSMutableDictionary *state = [self releaseState];
            if (![state[@"attempt"] isEqualToString:digest]) return;
            if (healthy) {
                if ([state[@"current"] length] && ![state[@"current"] isEqualToString:digest]) state[@"previous"] = state[@"current"];
                state[@"current"] = digest; [state removeObjectForKey:@"lastError"];
            } else state[@"lastError"] = @"候选 SDK 健康检查失败，重启后回退";
            [state removeObjectForKey:@"pending"]; [state removeObjectForKey:@"attempt"];
            if (![self saveReleaseState:state]) NSLog(@"[agov] SDK 健康状态保存失败，下次启动将安全回退");
        }
    });
}

- (BOOL)loadIfNeeded {
 @synchronized(self){
    if (_handle && !_loaded) return NO;
    if (_loaded) {
        return YES;
    }
    NSMutableArray<NSString *> *tried = [NSMutableArray array];
    NSFileManager *fm = [NSFileManager defaultManager];

    for (NSString *p in [self candidatePaths]) {
        if (![fm fileExistsAtPath:p]) {
            [tried addObject:[NSString stringWithFormat:@"%@(无文件)", p.lastPathComponent]];
            continue;
        }
        NSMutableDictionary *state = [self releaseState];
        NSString *pending = state[@"pending"];
        BOOL candidate = [pending length] && [p isEqualToString:[self pathForDigest:pending]];
        // 必须先落盘再 dlopen：Go 的初始化可能在 dlopen 内崩溃。
        if (candidate) {
            state[@"attempt"] = pending;
            if (![self saveReleaseState:state]) { [tried addObject:@"无法保存升级启动标记"]; continue; }
        }
        void *h = dlopen(p.fileSystemRepresentation, RTLD_NOW);
        if (h == NULL) {
            const char *detail = dlerror();NSString *failure=detail?[NSString stringWithUTF8String:detail]:@"?";
            if (candidate) {
                [state removeObjectForKey:@"pending"]; [state removeObjectForKey:@"attempt"];
                state[@"lastError"] = [@"候选 SDK dlopen 失败: " stringByAppendingString:failure]; [self saveReleaseState:state];
            }
            const char *e = failure.UTF8String;
            [tried addObject:[NSString stringWithFormat:@"%@(dlopen: %s)", p.lastPathComponent, e ?: "?"]];
            continue;
        }
        AGInvokeFn inv = (AGInvokeFn)dlsym(h, "agov_ios_invoke");
        AGFreeFn fr = (AGFreeFn)dlsym(h, "agov_ios_free");
        if (inv == NULL || fr == NULL) {
            // 文件在、也能 dlopen，但没有入口符号：说明 dylib 编错了（导出符号被剥掉）
            [tried addObject:[NSString stringWithFormat:@"%@(缺少入口 agov_ios_invoke/agov_ios_free)", p.lastPathComponent]];
            // Go 构造器可能已启动 runtime，不能 dlclose 后再加载另一个 Go 库。
            if (candidate) {
                [state removeObjectForKey:@"pending"]; [state removeObjectForKey:@"attempt"];
                state[@"lastError"] = @"候选 SDK 缺少导出入口，重启后回退"; [self saveReleaseState:state];
            }
            _handle = h; _loadError = @"SDK 缺少导出入口，请重启宿主以回退";
            return NO;
        }
        _handle = h;
        _invoke = inv;
        _free = fr;
        _loaded = YES;
        _path = [p copy];

        // dylib 里的 GoMain：与参考实现一致，加载后调一次（AX 引导等初始化在这里）
        AGGoMainFn gm = (AGGoMainFn)dlsym(h, "GoMain");
        if (gm != NULL) {
            gm();
        }
        if (candidate) [self confirmCandidate:pending];
        return YES;
    }

    _loadError = [@"未找到 libagovios.dylib —— " stringByAppendingString:[tried componentsJoinedByString:@"; "]];
    return NO;
 }
}

- (NSString *)invoke:(NSString *)method payload:(NSString *)payload {
    if (![self loadIfNeeded]) {
        _lastError = _loadError;
        return nil;
    }
    NSString *body = payload.length > 0 ? payload : @"{}";
    char *result = _invoke((char *)method.UTF8String, (char *)body.UTF8String);
    if (result == NULL) {
        _lastError = @"SDK 返回空指针（内部错误）";
        return nil;
    }
    NSString *out = [NSString stringWithUTF8String:result];
    _free(result); // 内存由 Go 分配，必须由 Go 释放
    return out ?: @"";
}

@end
