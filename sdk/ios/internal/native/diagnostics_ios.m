//go:build ios && cgo

// 同一套 AutoGo 私有 API 的环境与失败定位，不提供替代截图或替代控件树。
#import "runtime.h"
#import <objc/runtime.h>
#import <TargetConditionals.h>
#include <unistd.h>
#import <mach/mach.h>

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
    NSMutableDictionary *result=[NSMutableDictionary dictionaryWithDictionary:@{@"present":@(pointer!=NULL),@"image":image.dli_fname ? [NSString stringWithUTF8String:image.dli_fname] : @""}];
    // 只读取自己进程的函数入口，判断模拟器是否提供了直接失败的占位实现；不修改代码。
    if(pointer && strcmp(name,"CARenderServerRenderDisplay")==0){
        uint8_t bytes[256]={0};vm_size_t received=0;
        kern_return_t status=vm_read_overwrite(mach_task_self(),(vm_address_t)pointer,sizeof(bytes),(vm_address_t)bytes,&received);
        if(status==KERN_SUCCESS){
            NSMutableString *hex=[NSMutableString string];for(vm_size_t i=0;i<received;i++)[hex appendFormat:@"%02x",bytes[i]];result[@"entryBytesHex"]=hex;
            result[@"entryAddressHex"]=[NSString stringWithFormat:@"%llx",(unsigned long long)(uintptr_t)pointer];
#if defined(__arm64__)
            // 有界读取入口直接调用的函数，定位同一渲染路线在哪一层返回失败；不执行这些地址。
            NSMutableArray *branches=[NSMutableArray array];
            for(vm_size_t i=0;i+4<=received && branches.count<4;i+=4){
                uint32_t instruction=0;memcpy(&instruction,bytes+i,4);
                if(instruction==0xd65f03c0)break;
                if((instruction&0xfc000000)!=0x94000000 && (instruction&0xfc000000)!=0x14000000)continue;
                int32_t immediate=(int32_t)(instruction<<6);
                int64_t displacement=(int64_t)(immediate>>6)*4;
                uintptr_t target=(uintptr_t)pointer+i+displacement;Dl_info callee={0};
                if(!dladdr((void *)target,&callee) || callee.dli_fbase!=image.dli_fbase)continue;
                uint8_t code[256]={0};vm_size_t count=0;
                if(vm_read_overwrite(mach_task_self(),(vm_address_t)target,sizeof(code),(vm_address_t)code,&count)!=KERN_SUCCESS)continue;
                NSMutableString *codeHex=[NSMutableString string];for(vm_size_t j=0;j<count;j++)[codeHex appendFormat:@"%02x",code[j]];
                [branches addObject:@{@"addressHex":[NSString stringWithFormat:@"%llx",(unsigned long long)target],@"symbol":callee.dli_sname ? [NSString stringWithUTF8String:callee.dli_sname] : @"",@"bytesHex":codeHex}];
            }
            result[@"directBranches"]=branches;
#endif
        }
    }
    return result;
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
