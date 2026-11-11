//go:build ios && cgo

// 同一套 AutoGo 私有 API 的环境与失败定位，不提供替代截图或替代控件树。
#import "runtime.h"
#import <objc/runtime.h>
#import <TargetConditionals.h>
#include <unistd.h>

static NSDictionary *AGDiagnosticResult(ag_ios_result result,BOOL image) {
    NSData *data=result.data ? [NSData dataWithBytes:result.data length:result.length] : [NSData data];
    free(result.data);
    if(result.code!=0){NSString *error=[[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding] autorelease];return @{@"ok":@NO,@"code":@(result.code),@"error":error ?: @"原生调用失败"};}
    if(image)return @{@"ok":@YES,@"data":@{@"base64":[data base64EncodedStringWithOptions:0],@"bytes":@(data.length),@"width":@(result.width),@"height":@(result.height),@"mime":@"image/jpeg"}};
    id decoded=data.length ? [NSJSONSerialization JSONObjectWithData:data options:NSJSONReadingFragmentsAllowed error:NULL] : @{};
    NSMutableDictionary *out=[NSMutableDictionary dictionaryWithDictionary:@{@"ok":@YES,@"data":decoded ?: @{} }];
    if([decoded isKindOfClass:NSDictionary.class] && [decoded[@"nodes"] isKindOfClass:NSArray.class]){out[@"nodeCount"]=@([decoded[@"nodes"] count]);out[@"limit"]=@500;out[@"possiblyTruncated"]=@([decoded[@"nodes"] count]>=500);}
    return out;
}
static NSDictionary *AGDiagnosticCall(ag_ios_result (^call)(void),BOOL image) {
    @try{return AGDiagnosticResult(call(),image);}
    @catch(NSException *exception){return @{@"ok":@NO,@"error":exception.reason ?: @"原生异常",@"exception":exception.name ?: @"",@"stack":exception.callStackSymbols ?: @[]};}
}
static NSDictionary *AGDiagnosticSymbol(const char *name) {
    void *pointer=AGSymbol(name);Dl_info image={0};if(pointer)dladdr(pointer,&image);
    return @{@"present":@(pointer!=NULL),@"image":image.dli_fname ? [NSString stringWithUTF8String:image.dli_fname] : @""};
}
static NSString *AGDiagnosticClass(Class cls) {
    const char *image=cls ? class_getImageName(cls) : NULL;return image ? [NSString stringWithUTF8String:image] : @"";
}
ag_ios_result AGDiagnostics(void) {
    BOOL callerMain=NSThread.isMainThread;
    ag_ios_result geometry=AGOnMain(^{
        UIScreen *screen=UIScreen.mainScreen;CGRect native=screen.nativeBounds;
        NSMutableArray *names=[NSMutableArray arrayWithObject:@"LCD"];
        Class displayClass=NSClassFromString(@"CADisplay");SEL displays=NSSelectorFromString(@"displays"),name=NSSelectorFromString(@"name");
        if([displayClass respondsToSelector:displays]){
          id objects=((id(*)(id,SEL))objc_msgSend)(displayClass,displays);
          if([objects isKindOfClass:NSArray.class])for(id display in objects){
            if(names.count>=4)break;
            if([display respondsToSelector:name]){id value=((id(*)(id,SEL))objc_msgSend)(display,name);if([value isKindOfClass:NSString.class] && [value length] && ![names containsObject:value])[names addObject:value];}
          }
        }
        return AGJSON(@{@"width":@(native.size.width),@"height":@(native.size.height),@"scale":@(screen.scale),@"nativeScale":@(screen.nativeScale),@"displayNames":names});
    });
    NSDictionary *screenResult=AGDiagnosticResult(geometry,NO);
    NSDictionary *screen=[screenResult[@"data"] isKindOfClass:NSDictionary.class] ? screenResult[@"data"] : @{};
    NSDictionary *variables=NSProcessInfo.processInfo.environment;
    NSDictionary *environment=@{
        @"pid":@(getpid()),@"ppid":@(getppid()),@"uid":@(getuid()),@"euid":@(geteuid()),@"gid":@(getgid()),@"egid":@(getegid()),
        @"callerMainThread":@(callerMain),@"simulator":@(TARGET_OS_SIMULATOR),
        @"processName":NSProcessInfo.processInfo.processName ?: @"",@"bundlePath":NSBundle.mainBundle.bundlePath ?: @"",@"bundleID":NSBundle.mainBundle.bundleIdentifier ?: @"",
        @"simulatorRoot":variables[@"SIMULATOR_ROOT"] ?: @"",@"dyldRootPath":variables[@"DYLD_ROOT_PATH"] ?: @"",
        @"UIKitImage":AGDiagnosticClass(NSClassFromString(@"UIScreen")),@"AXUIElementImage":AGDiagnosticClass(NSClassFromString(@"AXUIElement")),
        @"symbols":@{@"CARenderServerRenderDisplay":AGDiagnosticSymbol("CARenderServerRenderDisplay"),@"IOSurfaceCreate":AGDiagnosticSymbol("IOSurfaceCreate"),@"UICreateCGImageFromIOSurface":AGDiagnosticSymbol("UICreateCGImageFromIOSurface"),@"AXFullKeyboardSet":AGDiagnosticSymbol("_AXSFullKeyboardAccessSetEnabled")}
    };
    NSDictionary *capture=AGDiagnosticCall(^{return AGCapture(@{@"maxSize":@512,@"jpeg":@YES,@"quality":@0.7});},YES);
    NSDictionary *probe=AGCaptureProbe(screen[@"displayNames"] ?: @[@"LCD"],[screen[@"width"] intValue],[screen[@"height"] intValue]);
    NSDictionary *axEnvironment=AGDiagnosticCall(^{return AGAXEnvironment(NO);},NO);
    NSDictionary *ax=AGDiagnosticCall(^{return AGUIAcc(@"uiacc.snapshot",@{@"limit":@500});},NO);
    return AGJSON(@{@"environment":environment,@"screen":screenResult,@"capture":capture,@"captureProbe":probe,@"axEnvironment":axEnvironment,@"ax":ax});
}
