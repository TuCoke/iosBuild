//go:build ios && cgo

#import "runtime.h"
#include <mach-o/dyld.h>
#include <stdlib.h>

// 独立执行体位于 .app/Runtime/app，NSBundle 的默认目录未必是外层 .app。
// 与样本一样先定位真正的应用根，供模型、动态库和自身应用信息共用。
NSString *AGApplicationBundlePath(void){
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
