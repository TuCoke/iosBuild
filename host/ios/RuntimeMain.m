// 独立任务进程只加载宿主指定的 SDK；不静态内置另一份 Go SDK。
// 主线程驱动 CFRunLoop，SDK 与解释器在后台执行，UIKit/AX 可回到主线程。
#import <Foundation/Foundation.h>
#import <CoreFoundation/CoreFoundation.h>
#include <dlfcn.h>
#include <unistd.h>
#include <stdlib.h>
#include <stdio.h>

typedef char *(*AGWorkerInvoke)(char *,char *);
typedef void (*AGWorkerFree)(char *);
typedef void (*AGWorkerInit)(void);
static void AGKeepLoop(void *info) {}
static dispatch_source_t AGWorkerWatchdog;
// 宿主意外退出后，脚本不能继续变成无人管理的后台进程；原生阻塞也必须有进程级期限。
static void AGStartWorkerWatchdog(double timeoutMs) {
    pid_t parent=getppid();
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW,(int64_t)((timeoutMs+2000)*NSEC_PER_MSEC)),dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0),^{
        fprintf(stderr,"任务进程超过硬期限\n");_exit(124);
    });
    AGWorkerWatchdog=dispatch_source_create(DISPATCH_SOURCE_TYPE_TIMER,0,0,dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0));
    dispatch_source_set_timer(AGWorkerWatchdog,dispatch_time(DISPATCH_TIME_NOW,NSEC_PER_SEC),NSEC_PER_SEC,100*NSEC_PER_MSEC);
    dispatch_source_set_event_handler(AGWorkerWatchdog,^{
        if(getppid()!=parent){fprintf(stderr,"宿主已退出，终止任务进程\n");_exit(125);}
    });
    dispatch_resume(AGWorkerWatchdog);
}
static int AGWriteWorkerResult(NSString *filename,NSDictionary *reply) {
    NSError *error=nil;NSData *data=[NSJSONSerialization dataWithJSONObject:reply options:0 error:&error];
    if(!data || ![data writeToFile:filename options:NSDataWritingAtomic error:&error]){fprintf(stderr,"结果保存失败: %s\n",error.localizedDescription.UTF8String);return 2;}
    fprintf(stdout,"%s\n",[[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding] UTF8String]);
    return [reply[@"ok"] boolValue] ? 0 : 1;
}
int main(int argc,char **argv) {
 @autoreleasepool {
    NSMutableDictionary *flags=[NSMutableDictionary dictionary];NSString *filename=nil;
    for(int i=1;i<argc;i++){
        NSString *argument=[NSString stringWithUTF8String:argv[i]];
        if([argument hasPrefix:@"--"] && i+1<argc){flags[argument]=[NSString stringWithUTF8String:argv[++i]];}
        else if(!filename)filename=argument;else{fprintf(stderr,"脚本参数无效\n");return 2;}
    }
    NSString *sdk=flags[@"--sdk"],*resultPath=flags[@"--result"];
    if(!sdk.length || !resultPath.length || !filename.length){fprintf(stderr,"缺少 --sdk / --result / 脚本文件\n");return 2;}
    NSData *parameters=[NSData dataWithContentsOfFile:flags[@"--params"] ?: @""];
    id params=parameters ? [NSJSONSerialization JSONObjectWithData:parameters options:0 error:NULL] : @{};
    if(![params isKindOfClass:NSDictionary.class]){fprintf(stderr,"参数 JSON 必须为对象\n");return 2;}
    // 这里只改变自己的工作目录，与参考执行入口一样以执行文件目录为基准。
    NSString *executable=[NSString stringWithUTF8String:argv[0]];
    if(chdir(executable.stringByDeletingLastPathComponent.fileSystemRepresentation)!=0){perror("chdir");return 2;}
    double timeoutMs=[flags[@"--timeout-ms"] doubleValue];
    if(timeoutMs<=0 || timeoutMs>300000){fprintf(stderr,"任务期限无效\n");return 2;}
    AGStartWorkerWatchdog(timeoutMs);
    CFRunLoopSourceContext sourceContext={0};sourceContext.perform=AGKeepLoop;
    CFRunLoopSourceRef source=CFRunLoopSourceCreate(kCFAllocatorDefault,0,&sourceContext);
    CFRunLoopAddSource(CFRunLoopGetMain(),source,kCFRunLoopDefaultMode);
    dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0),^{@autoreleasepool {
        void *library=dlopen(sdk.fileSystemRepresentation,RTLD_NOW|RTLD_LOCAL);
        AGWorkerInvoke invoke=library ? (AGWorkerInvoke)dlsym(library,"agov_ios_invoke") : NULL;
        AGWorkerFree freeResult=library ? (AGWorkerFree)dlsym(library,"agov_ios_free") : NULL;
        NSMutableDictionary *reply=[NSMutableDictionary dictionaryWithDictionary:@{@"ok":@NO,@"processId":@(getpid())}];
        if(!invoke || !freeResult){const char *error=dlerror();reply[@"error"]=[NSString stringWithFormat:@"SDK 加载失败: %s",error ?: "缺少 C ABI 入口"];exit(AGWriteWorkerResult(resultPath,reply));}
        AGWorkerInit initialize=(AGWorkerInit)dlsym(library,"GoMain");if(initialize)initialize();

        NSData *payload=[NSJSONSerialization dataWithJSONObject:@{@"path":filename,@"params":params,@"timeoutMs":@(timeoutMs)} options:0 error:NULL];
        NSString *payloadString=[[NSString alloc] initWithData:payload encoding:NSUTF8StringEncoding];
        char *response=invoke("script.executeFile",(char *)payloadString.UTF8String);
        if(response){
            NSData *data=[[NSString stringWithUTF8String:response] dataUsingEncoding:NSUTF8StringEncoding];
            id envelope=[NSJSONSerialization JSONObjectWithData:data options:0 error:NULL];freeResult(response);
            if([envelope isKindOfClass:NSDictionary.class]){
                if([envelope[@"data"] isKindOfClass:NSDictionary.class])[reply addEntriesFromDictionary:envelope[@"data"]];
                reply[@"ok"]=@([envelope[@"ok"] boolValue]);
                if(envelope[@"error"])reply[@"error"]=envelope[@"error"];
            }else reply[@"error"]=@"SDK 回执不是 JSON 对象";
        }else reply[@"error"]=@"SDK 返回空指针";
        reply[@"processId"]=@(getpid());
        // Go 运行时不 dlclose；任务进程退出时由系统回收其全部线程和原生资源。
        exit(AGWriteWorkerResult(resultPath,reply));
    }});
    CFRunLoopRun();CFRunLoopSourceInvalidate(source);CFRelease(source);return 2;
 }
}
