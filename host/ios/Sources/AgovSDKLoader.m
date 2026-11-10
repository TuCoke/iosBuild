//
// AgovSDKLoader.m —— SDK 动态加载器（壳方案的核心）
//
// 【为什么需要它】
// 把 SDK/解释器编进主程序，改一行 SDK 就要重新打包安装；
// 编成独立 dylib 后，宿主只做一件事：找到它、签名、dlopen、调用。
// 这样"换 SDK"等于"换一个文件"，不需要重装 App。
//
// 【两种来源，按优先级】
//   1. 沙盒 <Documents>/Plugins/libagovios.dylib —— 可写、可下发、可替换（热更）
//   2. app 包内 libagovios.dylib                —— 随包分发的版本（出厂）
//   3. 都没有 → 回退到编进主程序的静态实现（libagovios.a）
//
// 【为什么保留静态回退】
// 动态库加载失败（签名不对、架构不对、权限不够）时，App 不能变成砖头。
// 静态那份永远在，只是版本旧一点 —— 这是"热更失败自动回退"的保险。
//
// 【签名】
// iOS 只允许加载有合法签名的 dylib。下发的新库必须先签名：
//   ldid -S<entitlements> libagovios.dylib      （越狱/TrollStore 环境）
// 未签名的库 dlopen 会返回 NULL，这里会明确把原因报给上层。
//
#import <Foundation/Foundation.h>
#import <dlfcn.h>

#include "libagovios.h"

typedef char *(*AgInvokeFn)(const char *, const char *);
typedef void (*AgFreeFn)(char *);

static AgInvokeFn gDynamicInvoke;
static AgFreeFn gDynamicFree;
static NSString *gLoadedPath;
static NSString *gLoadError;
static dispatch_once_t gOnce;

// AGSDKPluginsDir 返回可写插件目录（不存在则创建）。
static NSString *AGSDKPluginsDir(void) {
    NSString *docs = NSSearchPathForDirectoriesInDomains(NSDocumentDirectory, NSUserDomainMask, YES).firstObject;
    if (!docs.length) return nil;
    NSString *dir = [docs stringByAppendingPathComponent:@"Plugins"];
    [[NSFileManager defaultManager] createDirectoryAtPath:dir
                              withIntermediateDirectories:YES
                                               attributes:nil
                                                    error:NULL];
    return dir;
}

// AGSDKLoadOnce 依次尝试沙盒插件、包内 dylib；成功则记下函数指针。
static void AGSDKLoadOnce(void) {
    dispatch_once(&gOnce, ^{
        NSMutableArray<NSString *> *candidates = [NSMutableArray array];
        NSString *plugins = AGSDKPluginsDir();
        if (plugins.length) [candidates addObject:[plugins stringByAppendingPathComponent:@"libagovios.dylib"]];
        NSString *bundle = [[NSBundle mainBundle] bundlePath];
        if (bundle.length) {
            [candidates addObject:[bundle stringByAppendingPathComponent:@"libagovios.dylib"]];
            [candidates addObject:[bundle stringByAppendingPathComponent:@"Frameworks/libagovios.dylib"]];
        }
        for (NSString *path in candidates) {
            if (![[NSFileManager defaultManager] fileExistsAtPath:path]) continue;
            void *handle = dlopen(path.UTF8String, RTLD_LAZY | RTLD_LOCAL);
            if (!handle) {
                gLoadError = [NSString stringWithFormat:@"dlopen 失败: %s", dlerror()];
                continue;
            }
            AgInvokeFn invoke = (AgInvokeFn)dlsym(handle, "agov_ios_invoke");
            AgFreeFn freeFn = (AgFreeFn)dlsym(handle, "agov_ios_free");
            if (!invoke || !freeFn) {
                gLoadError = [NSString stringWithFormat:@"dylib 缺少入口: %@", path.lastPathComponent];
                dlclose(handle);
                continue;
            }
            gDynamicInvoke = invoke;
            gDynamicFree = freeFn;
            gLoadedPath = path;
            gLoadError = nil;
            break;
        }
    });
}

// AGSDKInvoke 是壳唯一需要的入口：动态库在就用动态库，否则用链进来的静态实现。
char *AGSDKInvoke(const char *method, const char *payload) {
    AGSDKLoadOnce();
    if (gDynamicInvoke) return gDynamicInvoke(method, payload);
    return agov_ios_invoke(method, payload);
}

void AGSDKFree(char *p) {
    AGSDKLoadOnce();
    if (gDynamicFree) { gDynamicFree(p); return; }
    agov_ios_free(p);
}

// AGSDKIsDynamic 说明当前是不是动态库在干活（诊断界面会显示）。
int AGSDKIsDynamic(void) { AGSDKLoadOnce(); return gDynamicInvoke ? 1 : 0; }

// AGSDKLoadedPath 返回当前加载的 dylib 路径（静态实现返回空串）。
char *AGSDKLoadedPath(void) {
    AGSDKLoadOnce();
    return (char *)(gLoadedPath.length ? gLoadedPath.UTF8String : "");
}

// AGSDKLoadError 返回最近一次加载失败的原因（成功时为空串）。
char *AGSDKLoadError(void) {
    AGSDKLoadOnce();
    return (char *)(gLoadError.length ? gLoadError.UTF8String : "");
}

// AGSDKReload 把新下载的 dylib 放到位并重新加载。
//
// 注意：dlopen 过的库不能真正卸载（引用计数与 ObjC 运行时都不允许干净卸载），
// 所以"重载"只在**当前还没加载动态库**时生效；已经加载过就要重启进程。
// 这是 iOS 的硬限制，不是实现偷懒 —— 上层应该在替换后调用重启（utils.restart_self）。
int AGSDKReload(void) {
    if (gDynamicInvoke) return 0; // 已加载，需要重启进程
    gOnce = 0;                    // 重置一次性开关，下次调用重新探测
    AGSDKLoadOnce();
    return gDynamicInvoke ? 1 : 0;
}
