// AGSDKLoader.m
#import "AGSDKLoader.h"
#import <dlfcn.h>
#import <CommonCrypto/CommonDigest.h>

@implementation AGSDKLoader {
    void *_handle;
    AGInvokeFn _invoke;
    AGFreeFn _free;
    dispatch_source_t _healthTimer;
    dispatch_queue_t _healthQueue;
    BOOL _healthProbePending, _healthProbeBlocked;
    NSUInteger _healthProbeSerial;
    NSString *_healthDigest;
    NSString *_lastHealthNotice;
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
    for (NSString *key in @[@"current",@"previous",@"pending",@"attempt",@"rejected"]) {
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
        NSString *digest = state[key];
        if(digest.length && [digest isEqualToString:state[@"rejected"]])continue;
        NSString *path = [self pathForDigest:digest];
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
// 连续三次通过才确认候选，确认后仍观察。单次忙请求不再代表 SDK 失败。
// 探测只读 ABI/映像/目录，不要求截图、AX 等可选授权能力可用。
- (void)finishHealthProbe:(NSUInteger)serial reply:(NSDictionary *)reply timedOut:(BOOL)timedOut {
    if(serial!=_healthProbeSerial)return;
    if(!_healthProbePending){if(!timedOut)_healthProbeBlocked=NO;return;}
    _healthProbePending=NO;_healthProbeBlocked=timedOut;
    NSDictionary *data=[reply[@"data"] isKindOfClass:NSDictionary.class]?reply[@"data"]:@{};
    BOOL ready=!timedOut && [reply[@"ok"] boolValue] && [data[@"ready"] boolValue] && [data[@"protocol"] integerValue]==2;
    NSDictionary *report=timedOut?@{@"error":@"SDK 健康调用超过八秒，等待原调用结束，禁止堆积新探测"}:reply ?: @{};
    NSDictionary *observation=@{@"digest":_healthDigest ?: @"",@"ready":@(ready),@"timedOut":@(timedOut),@"report":report};
    NSData *payload=[NSJSONSerialization dataWithJSONObject:observation options:0 error:NULL];
    NSString *body=[[NSString alloc] initWithData:payload encoding:NSUTF8StringEncoding];
    char *raw=self->_invoke("sdk.health.record",(char *)body.UTF8String);
    if(raw){
        NSData *resultData=[[NSString stringWithUTF8String:raw] dataUsingEncoding:NSUTF8StringEncoding];self->_free(raw);
        id recorded=[NSJSONSerialization JSONObjectWithData:resultData options:0 error:NULL];
        BOOL saved=[recorded isKindOfClass:NSDictionary.class] && [recorded[@"ok"] boolValue];
        if(saved && [recorded[@"data"][@"health"][@"successes"] integerValue]>=3 && ready)
            dispatch_source_set_timer(_healthTimer,dispatch_time(DISPATCH_TIME_NOW,30*NSEC_PER_SEC),30*NSEC_PER_SEC,NSEC_PER_SEC);
        if(saved){
            NSDictionary *state=recorded[@"data"];
            BOOL needsRestart=[state[@"needsRestart"] boolValue];
            NSInteger successes=[state[@"health"][@"successes"] integerValue];
            NSString *notice=needsRestart?@"SDK 健康异常，请重启宿主以回退":ready?(successes>=3?@"SDK 持续健康检查正常":[NSString stringWithFormat:@"SDK 健康观察中（%ld/3）",(long)successes]):@"SDK 健康探测未通过，继续观察";
            if(![notice isEqualToString:_lastHealthNotice]){
                _lastHealthNotice=notice;
                dispatch_async(dispatch_get_main_queue(),^{[NSNotificationCenter.defaultCenter postNotificationName:@"AGSDKHealthDidChange" object:self userInfo:@{@"message":notice,@"needsRestart":@(needsRestart)}];});
            }
        }
        if(!saved)NSLog(@"[sdk-health] 健康状态未保存，请检查持久目录");
        else if([recorded[@"data"][@"needsRestart"] boolValue])NSLog(@"[sdk-health] SDK 健康异常，已安排下次启动回退，需要重启宿主");
    }
}
- (void)confirmCandidate:(NSString *)digest {
    _healthDigest=[digest copy];
    _healthQueue=dispatch_queue_create("cc.agov.sdk.health",DISPATCH_QUEUE_SERIAL);
    _healthTimer=dispatch_source_create(DISPATCH_SOURCE_TYPE_TIMER,0,0,_healthQueue);
    dispatch_source_set_timer(_healthTimer,dispatch_time(DISPATCH_TIME_NOW,10*NSEC_PER_SEC),10*NSEC_PER_SEC,NSEC_PER_SEC);
    __weak AGSDKLoader *weakSelf=self;
    dispatch_source_set_event_handler(_healthTimer,^{
        AGSDKLoader *strong=weakSelf;if(!strong || strong->_healthProbePending || strong->_healthProbeBlocked)return;
        strong->_healthProbePending=YES;NSUInteger serial=++strong->_healthProbeSerial;
        dispatch_async(dispatch_get_global_queue(QOS_CLASS_UTILITY,0),^{
            char *raw=strong->_invoke("sdk.health","{}");
            NSData *data=raw?[[NSString stringWithUTF8String:raw] dataUsingEncoding:NSUTF8StringEncoding]:nil;
            if(raw)strong->_free(raw);
            id decoded=data?[NSJSONSerialization JSONObjectWithData:data options:0 error:NULL]:nil;
            NSDictionary *reply=[decoded isKindOfClass:NSDictionary.class]?decoded:@{};
            dispatch_async(strong->_healthQueue,^{[strong finishHealthProbe:serial reply:reply timedOut:NO];});
        });
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW,8*NSEC_PER_SEC),strong->_healthQueue,^{
            if(strong->_healthProbeSerial==serial && strong->_healthProbePending)[strong finishHealthProbe:serial reply:nil timedOut:YES];
        });
    });
    dispatch_resume(_healthTimer);
}
// 启动阶段还未开放 SDK 调用，版本状态可由加载器串行调整。
- (NSString *)digestForPath:(NSString *)path state:(NSDictionary *)state {
    for(NSString *key in @[@"pending",@"current",@"previous"]){
        NSString *digest=state[key];if(digest.length && [path isEqualToString:[self pathForDigest:digest]])return digest;
    }
    return nil;
}
- (void)rejectDigest:(NSString *)digest reason:(NSString *)reason needsRestart:(BOOL)needsRestart {
    NSMutableDictionary *state=[self releaseState];
    if(digest.length){
        state[@"rejected"]=digest;
        if([state[@"current"] isEqualToString:digest]){
            NSString *previous=state[@"previous"];
            if(previous.length && ![previous isEqualToString:digest])state[@"current"]=previous;else [state removeObjectForKey:@"current"];
            [state removeObjectForKey:@"previous"];
        }
        if([state[@"pending"] isEqualToString:digest])[state removeObjectForKey:@"pending"];
        if([state[@"attempt"] isEqualToString:digest])[state removeObjectForKey:@"attempt"];
    }
    state[@"lastError"]=reason;state[@"needsRestart"]=@(needsRestart);
    if(![self saveReleaseState:state])NSLog(@"[sdk-health] 无法保存加载失败状态");
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
        NSString *loadingDigest=[self digestForPath:p state:state];
        BOOL candidate = [pending length] && [p isEqualToString:[self pathForDigest:pending]];
        // 必须先落盘再 dlopen：Go 的初始化可能在 dlopen 内崩溃。
        if (candidate) {
            state[@"attempt"] = pending;
            if (![self saveReleaseState:state]) { [tried addObject:@"无法保存升级启动标记"]; continue; }
        }
        void *h = dlopen(p.fileSystemRepresentation, RTLD_NOW);
        if (h == NULL) {
            const char *detail = dlerror();NSString *failure=detail?[NSString stringWithUTF8String:detail]:@"?";
            [self rejectDigest:loadingDigest reason:[@"SDK dlopen 失败: " stringByAppendingString:failure] needsRestart:NO];
            const char *e = failure.UTF8String;
            [tried addObject:[NSString stringWithFormat:@"%@(dlopen: %s)", p.lastPathComponent, e ?: "?"]];
            continue;
        }
        AGInvokeFn inv = (AGInvokeFn)dlsym(h, "agov_ios_invoke");
        AGFreeFn fr = (AGFreeFn)dlsym(h, "agov_ios_free");
        AGGoMainFn gm = (AGGoMainFn)dlsym(h, "GoMain");
        if (inv == NULL || fr == NULL || gm == NULL) {
            // 文件在、也能 dlopen，但没有入口符号：说明 dylib 编错了（导出符号被剥掉）
            [tried addObject:[NSString stringWithFormat:@"%@(缺少入口 agov_ios_invoke/agov_ios_free)", p.lastPathComponent]];
            // Go 构造器可能已启动 runtime，不能 dlclose 后再加载另一个 Go 库。
            [self rejectDigest:loadingDigest reason:@"SDK 缺少导出入口，重启后回退" needsRestart:YES];
            _handle = h; _loadError = @"SDK 缺少导出入口，请重启宿主以回退";
            return NO;
        }
        _handle = h;
        _invoke = inv;
        _free = fr;
        _loaded = YES;
        _path = [p copy];

        // dylib 里的 GoMain：与参考实现一致，加载后调一次（AX 引导等初始化在这里）
        gm();
        [self confirmCandidate:loadingDigest];
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
