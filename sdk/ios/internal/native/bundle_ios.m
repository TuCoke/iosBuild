//go:build ios && cgo

#import "runtime.h"
#include <mach-o/dyld.h>
#include <stdlib.h>

// 独立执行体可能已复制到 Documents/Worker，不能再从执行体目录猜测 App 根。
// 优先使用宿主传入且包含 Info.plist 的 --bundle，供模型、动态库及应用信息共用。
NSString *AGApplicationBundlePath(void){
    NSArray<NSString *> *arguments=NSProcessInfo.processInfo.arguments;
    for(NSUInteger i=1;i+1<arguments.count;i++) {
        if(![arguments[i] isEqualToString:@"--bundle"])continue;
        NSString *candidate=[arguments[i+1] stringByStandardizingPath];
        if(candidate.isAbsolutePath && [candidate.pathExtension.lowercaseString isEqualToString:@"app"] &&
           [NSFileManager.defaultManager fileExistsAtPath:[candidate stringByAppendingPathComponent:@"Info.plist"]])return candidate;
    }

    NSString *bundle=NSBundle.mainBundle.bundlePath;
    if([bundle.pathExtension.lowercaseString isEqualToString:@"app"])return bundle;
    uint32_t size=0;_NSGetExecutablePath(NULL,&size);if(!size || size>1024*1024)return bundle;
    char *buffer=malloc(size);if(!buffer)return bundle;
    int status=_NSGetExecutablePath(buffer,&size);NSString *executable=status==0?[NSString stringWithUTF8String:buffer]:nil;free(buffer);
    executable=[[executable stringByStandardizingPath] stringByResolvingSymlinksInPath];
    NSRange app=[executable rangeOfString:@".app/" options:NSBackwardsSearch];
    if(executable && app.location!=NSNotFound)return [executable substringToIndex:app.location+4];
    return bundle;
}
