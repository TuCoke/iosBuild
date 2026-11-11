// AGSDKLoader.m
#import "AGSDKLoader.h"
#import <dlfcn.h>

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

/// 候选路径，顺序即优先级：
/// 1) 沙盒 Plugins —— sdk.install 热更下来的产物，优先于包内
/// 2) App 根目录   —— 与参考实现同位置（AutoGoRunner.app/libgoeval.dylib 在 Runtime/ 下）
/// 3) Frameworks/  —— iOS 推荐的动态库位置
- (NSArray<NSString *> *)candidatePaths {
    NSMutableArray<NSString *> *list = [NSMutableArray array];
    NSString *docs = NSSearchPathForDirectoriesInDomains(NSDocumentDirectory, NSUserDomainMask, YES).firstObject;
    if (docs.length > 0) {
        [list addObject:[docs stringByAppendingPathComponent:@"Plugins/libagovios.dylib"]];
    }
    NSString *bundle = [NSBundle mainBundle].bundlePath;
    [list addObject:[bundle stringByAppendingPathComponent:@"libagovios.dylib"]];
    [list addObject:[bundle stringByAppendingPathComponent:@"Frameworks/libagovios.dylib"]];
    return list;
}

- (BOOL)loadIfNeeded {
 @synchronized(self){
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
        void *h = dlopen(p.fileSystemRepresentation, RTLD_NOW);
        if (h == NULL) {
            const char *e = dlerror();
            [tried addObject:[NSString stringWithFormat:@"%@(dlopen: %s)", p.lastPathComponent, e ?: "?"]];
            continue;
        }
        AGInvokeFn inv = (AGInvokeFn)dlsym(h, "agov_ios_invoke");
        AGFreeFn fr = (AGFreeFn)dlsym(h, "agov_ios_free");
        if (inv == NULL || fr == NULL) {
            // 文件在、也能 dlopen，但没有入口符号：说明 dylib 编错了（导出符号被剥掉）
            [tried addObject:[NSString stringWithFormat:@"%@(缺少入口 agov_ios_invoke/agov_ios_free)", p.lastPathComponent]];
            dlclose(h);
            continue;
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
