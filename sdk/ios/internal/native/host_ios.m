//go:build ios && cgo

// 进程与系统工具：按名查 pid、读进程内存/CPU、自重启，以及 Toast/Alert/执行外部程序。
//
// 三条硬约束写在这里，免得后来人踩：
//   1. 弹窗类（Toast/Alert/InputAlert）必须在主线程，而且 Alert 要等用户点按钮 ——
//      不能用 AGOnMain（它有 3 秒上限，会误判超时），这里用信号量自己等。
//   2. 启动外部进程（ExecBinary / RestartSelf）在普通签名下会被系统拒绝，
//      和 platform-application 权限绑死；失败必须如实返回 -1，不能假装成功。
//   3. iOS SDK 不提供 <sys/proc_info.h>，连 <libproc.h> 都会把它带进来；
//      所以进程相关的 C 函数在这里自己声明，只依赖 <sys/resource.h>。
#import "runtime.h"
#include <sys/resource.h>
#include <spawn.h>
#include <signal.h>
#include <fcntl.h>
#include <errno.h>
#include <mach-o/dyld.h>
#include <mach/mach_time.h>
#include <unistd.h>
#include <sys/wait.h>
#include <stdlib.h>
#include <string.h>

extern char **environ;

// libproc 的三个入口 + rusage。自己声明，绕开 proc_info.h 缺失的问题。
extern int proc_pid_rusage(int pid, int flavor, rusage_info_t *buffer);
extern int proc_listallpids(void *buffer, int buffersize);
extern int proc_name(int pid, void *buffer, uint32_t buffersize);
#ifndef RUSAGE_INFO_V2
#define RUSAGE_INFO_V2 2
#endif

// AGFindPID 遍历所有进程，按可执行名匹配；找不到返回 0。
static int AGFindPID(NSString *name) {
    if (name.length == 0) return 0;
    int capacity = 4096;
    pid_t *pids = calloc((size_t)capacity, sizeof(pid_t));
    if (!pids) return 0;
    int count = proc_listallpids(pids, capacity * (int)sizeof(pid_t));
    int found = 0;
    if (count > 0) {
        if (count > capacity) count = capacity;
        for (int i = 0; i < count; i++) {
            char processName[64] = {0};
            if (proc_name(pids[i], processName, sizeof(processName)) <= 0) continue;
            if (strcmp(processName, name.UTF8String) == 0) { found = pids[i]; break; }
        }
    }
    free(pids);
    return found;
}

// 由本进程创建的子进程只由统一的 waitpid 回收，避免僵尸进程以及多个等待者争抢状态。
static NSMutableDictionary *AGChildren(void) {
    static NSMutableDictionary *children;static dispatch_once_t once;
    dispatch_once(&once,^{children=[NSMutableDictionary new];});return children;
}
static void AGWatchChild(pid_t child) {
    NSMutableDictionary *record=[NSMutableDictionary dictionaryWithDictionary:@{@"finished":@NO,@"code":@0}];
    @synchronized(AGChildren()) {
        if(AGChildren().count>=256){for(NSNumber *key in [AGChildren() allKeys]){if([AGChildren()[key][@"finished"] boolValue])[AGChildren() removeObjectForKey:key];}}
        AGChildren()[@(child)]=record;
    }
    dispatch_async(dispatch_get_global_queue(QOS_CLASS_UTILITY,0),^{
        int status=0;pid_t waited;do{waited=waitpid(child,&status,0);}while(waited<0&&errno==EINTR);
        int code=waited<0 ? -1 : (WIFEXITED(status) ? WEXITSTATUS(status) : (WIFSIGNALED(status) ? -WTERMSIG(status) : -1));
        @synchronized(AGChildren()){record[@"code"]=@(code);record[@"finished"]=@YES;}
    });
}
static BOOL AGChildFinished(pid_t child) {
    @synchronized(AGChildren()){NSDictionary *record=AGChildren()[@(child)];return record && [record[@"finished"] boolValue];}
}

ag_ios_result AGSystem(NSString *op, NSDictionary *args) {
    if ([op isEqualToString:@"system.pid"]) {
        return AGJSON(@{@"pid":@(AGFindPID(args[@"name"]))});
    }
    if ([op isEqualToString:@"system.memory"]) {
        int pid = [args[@"pid"] intValue];
        if (pid <= 0) return AGError(2,@"pid 无效");
        // 用 rusage 的常驻内存（ri_resident_size）：proc_pidinfo 那条路在 iOS SDK 上编不过。
        struct rusage_info_v2 info;
        if (proc_pid_rusage(pid, RUSAGE_INFO_V2, (rusage_info_t *)&info) != 0) {
            return AGError(3,@"读不到该进程信息");
        }
        return AGJSON(@{@"kb":@((long long)info.ri_resident_size / 1024)});
    }
    if ([op isEqualToString:@"system.cpu"]) {
        int pid = [args[@"pid"] intValue];
        if (pid <= 0) return AGError(2,@"pid 无效");
        struct rusage_info_v2 a, b;
        if (proc_pid_rusage(pid, RUSAGE_INFO_V2, (rusage_info_t *)&a) != 0) return AGError(3,@"读不到 CPU 信息");
        usleep(200 * 1000);
        if (proc_pid_rusage(pid, RUSAGE_INFO_V2, (rusage_info_t *)&b) != 0) return AGError(3,@"读不到 CPU 信息");
        // 单位是 mach absolute time，换算成 200ms 窗口内的占比。
        uint64_t used = (b.ri_user_time + b.ri_system_time) - (a.ri_user_time + a.ri_system_time);
        mach_timebase_info_data_t tb;
        mach_timebase_info(&tb);
        double ns = (double)used * (double)tb.numer / (double)tb.denom;
        double percent = ns / (200.0 * 1000000.0) * 100.0;
        return AGJSON(@{@"percent":@(percent)});
    }
    if ([op isEqualToString:@"system.restart_self"]) {
        char path[4096] = {0};
        uint32_t size = sizeof(path);
        if (_NSGetExecutablePath(path, &size) != 0) return AGError(3,@"无法取到自身可执行路径");
        pid_t child = 0;
        char *argv[] = {path, NULL};
        if (posix_spawn(&child, path, NULL, NULL, argv, environ) != 0) {
            return AGError(3,@"重启失败：系统拒绝启动进程");
        }
        // 新实例已经拉起，旧进程退出前给一点时间让句柄交接。
        usleep(200 * 1000);
        exit(0);
    }
    return AGError(1,@"未知系统操作");
}

// ---- 弹窗 ----

// AGRunOnMainAndWait 把 block 丢到主线程执行并等到完成（不设短上限：
// Alert 要等人点按钮，用 AGOnMain 的 3 秒会直接判超时）。
static void AGRunOnMainAndWait(dispatch_block_t block) {
    if ([NSThread isMainThread]) { block(); return; }
    dispatch_semaphore_t done = dispatch_semaphore_create(0);
    dispatch_async(dispatch_get_main_queue(), ^{ block(); dispatch_semaphore_signal(done); });
    dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);
#if !OS_OBJECT_USE_OBJC
    dispatch_release(done);
#endif
}

// 弹窗结果由按钮回调完成，不能把“已展示”当作“用户已确认”。
@interface AGAlertWait : NSObject {
@public NSCondition *condition;
    BOOL completed,presented;
    NSInteger index;
    NSString *text;
    UIAlertController *alert;
}
@end
@implementation AGAlertWait
- (id)init {if((self=[super init])){condition=[NSCondition new];index=-1;text=[@"" copy];}return self;}
- (void)dealloc {[condition release];[text release];[alert release];[super dealloc];}
@end
static void AGCompleteAlert(AGAlertWait *state,NSInteger index,NSString *text) {
    [state->condition lock];
    if(!state->completed){state->completed=YES;state->index=index;[state->text release];state->text=[(text ?: @"") copy];[state->condition broadcast];}
    [state->condition unlock];
    [state->alert release];state->alert=nil;
}

ag_ios_result AGUtils(NSString *op, NSDictionary *args) {
    if ([op isEqualToString:@"utils.toast"]) {
        NSString *message = args[@"message"] ?: @"";
        double duration = [args[@"duration"] doubleValue];
        if (duration <= 0 || duration > 60000) duration = 1500;
        __block ag_ios_result result = AGJSON(@{@"submitted":@YES});
        AGRunOnMainAndWait(^{
            UIWindow *window = UIApplication.sharedApplication.keyWindow;
            if (!window) { result = AGError(3,@"没有可用的窗口"); return; }
            UILabel *label = [[UILabel alloc] initWithFrame:CGRectZero];
            label.text = message;
            label.font = [UIFont systemFontOfSize:15];
            label.textColor = UIColor.whiteColor;
            label.backgroundColor = [UIColor colorWithWhite:0 alpha:0.8];
            label.textAlignment = NSTextAlignmentCenter;
            label.numberOfLines = 0;
            [label sizeToFit];
            CGRect frame = label.frame;
            frame.size.width += 24; frame.size.height += 16;
            CGFloat x = [args[@"x"] doubleValue], y = [args[@"y"] doubleValue];
            frame.origin = CGPointMake(x > 0 ? x : (window.bounds.size.width - frame.size.width) / 2,
                                       y > 0 ? y : window.bounds.size.height * 0.7);
            label.frame = frame;
            label.layer.cornerRadius = 8;
            label.clipsToBounds = YES;
            [window addSubview:label];
            dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(duration * NSEC_PER_MSEC)),
                           dispatch_get_main_queue(), ^{ [label removeFromSuperview]; [label release]; });
        });
        return result;
    }

    if ([op isEqualToString:@"utils.alert"] || [op isEqualToString:@"utils.input_alert"]) {
        if([NSThread isMainThread])return AGError(2,@"同步弹窗必须从脚本工作线程调用");
        BOOL wantsInput=[op isEqualToString:@"utils.input_alert"];
        AGAlertWait *state=[AGAlertWait new];
        dispatch_async(dispatch_get_main_queue(),^{
            [state->condition lock];BOOL expired=state->completed;[state->condition unlock];if(expired)return;
            UIViewController *root=UIApplication.sharedApplication.keyWindow.rootViewController;
            while(root.presentedViewController)root=root.presentedViewController;
            if(!root){AGCompleteAlert(state,-1,@"");return;}
            UIAlertController *alert=[UIAlertController alertControllerWithTitle:args[@"title"] ?: @"" message:args[@"content"] ?: @"" preferredStyle:UIAlertControllerStyleAlert];
            state->alert=[alert retain];
            if(wantsInput)[alert addTextFieldWithConfigurationHandler:^(UITextField *field){field.placeholder=args[@"placeholder"] ?: @"";field.text=args[@"default"] ?: @"";}];
            NSArray *buttons=[args[@"buttons"] isKindOfClass:NSArray.class] ? args[@"buttons"] : @[];
            NSString *first=buttons.count>0 && [buttons[0] length] ? buttons[0] : @"确定";
            [alert addAction:[UIAlertAction actionWithTitle:first style:UIAlertActionStyleDefault handler:^(UIAlertAction *action){AGCompleteAlert(state,0,wantsInput ? alert.textFields.firstObject.text : @"");}]];
            if(buttons.count>1 && [buttons[1] length])[alert addAction:[UIAlertAction actionWithTitle:buttons[1] style:UIAlertActionStyleCancel handler:^(UIAlertAction *action){AGCompleteAlert(state,1,wantsInput ? alert.textFields.firstObject.text : @"");}]];
            [root presentViewController:alert animated:YES completion:nil];
            [state->condition lock];state->presented=YES;[state->condition unlock];
        });
        NSDate *deadline=[NSDate dateWithTimeIntervalSinceNow:300];
        [state->condition lock];while(!state->completed && [state->condition waitUntilDate:deadline]){}
        BOOL timedOut=!state->completed;state->completed=YES;
        NSInteger index=state->index;NSString *text=[state->text copy];BOOL presented=state->presented;
        [state->condition unlock];
        if(timedOut)dispatch_async(dispatch_get_main_queue(),^{[state->alert dismissViewControllerAnimated:YES completion:nil];[state->alert release];state->alert=nil;});
        ag_ios_result result=timedOut ? AGError(6,@"等待弹窗确认超时") : (!presented ? AGError(3,@"没有可用弹窗宿主") : (wantsInput ? AGJSON(@{@"text":text,@"confirm":@(index==0),@"index":@(index)}) : AGJSON(@{@"index":@(index)})));
        [text release];[state release];return result;
    }

    // ---- 后台进程（frpc 这类常驻进程）----
    //
    // 与 utils.exec 的区别：exec 会 waitpid 阻塞到子进程退出，
    // 常驻进程必须"启动即返回 pid"，所以这里单独一条：
    //   utils.spawn → posix_spawn + 把 stdout/stderr 重定向到日志文件，返回 pid
    //   utils.kill  → 先 SIGTERM，宽限期后再 SIGKILL
    //   utils.alive → kill(pid,0) 判断存活
    if ([op isEqualToString:@"utils.spawn"]) {
        NSString *path = args[@"path"];
        if (path.length == 0) return AGError(2,@"缺少可执行文件路径");
        // 检查失败时把"到底哪里不对"说清楚：不存在？不是普通文件？没有执行位？
        NSFileManager *fm = [NSFileManager defaultManager];
        if (![fm fileExistsAtPath:path]) {
            return AGError(3,[NSString stringWithFormat:@"文件不存在: %@", path]);
        }
        if (![fm isExecutableFileAtPath:path]) {
            NSDictionary *attrs = [fm attributesOfItemAtPath:path error:NULL];
            unsigned long mode = [attrs[NSFilePosixPermissions] unsignedLongValue];
            return AGError(3,[NSString stringWithFormat:@"没有执行权限: %@ (mode=%o, size=%llu)",
                                    path, (unsigned int)(mode & 0777), [attrs[NSFileSize] unsignedLongLongValue]]);
        }
        NSArray *extra = args[@"args"];
        NSMutableArray *argvList = [NSMutableArray arrayWithObject:path];
        if ([extra isKindOfClass:[NSArray class]]) {
            for (id item in extra) [argvList addObject:[item description]];
        }
        char **argv = calloc(argvList.count + 1, sizeof(char *));
        for (NSUInteger i = 0; i < argvList.count; i++) argv[i] = strdup([argvList[i] UTF8String]);

        // 日志重定向：宿主只读这个文件就能看到 frpc 的输出。
        NSString *logPath = args[@"logPath"];
        posix_spawn_file_actions_t actions;
        posix_spawn_file_actions_init(&actions);
        int logFD = -1;
        if (logPath.length) {
            logFD = open(logPath.UTF8String, O_CREAT | O_WRONLY | O_APPEND, 0644);
            if (logFD >= 0) {
                posix_spawn_file_actions_adddup2(&actions, logFD, STDOUT_FILENO);
                posix_spawn_file_actions_adddup2(&actions, logFD, STDERR_FILENO);
            }
        }

        pid_t child = 0;
        int rc = posix_spawn(&child, path.UTF8String, &actions, NULL, argv, environ);
        posix_spawn_file_actions_destroy(&actions);
        if (logFD >= 0) close(logFD);
        for (NSUInteger i = 0; i < argvList.count; i++) free(argv[i]);
        free(argv);
        if (rc != 0) return AGError(3,[NSString stringWithFormat:@"系统拒绝启动进程(rc=%d)，需要 platform-application / run-unsigned-code 权限", rc]);
        AGWatchChild(child);
        return AGJSON(@{@"pid":@((int)child)});
    }
    if ([op isEqualToString:@"utils.wait"]) {
        NSNumber *pid=args[@"pid"];
        @synchronized(AGChildren()) {
            NSDictionary *record=AGChildren()[pid];
            if(!record)return AGError(2,@"只能等待本宿主创建的子进程");
            return AGJSON(record);
        }
    }
    if ([op isEqualToString:@"utils.kill"]) {
        int pid = [args[@"pid"] intValue];
        if (pid <= 0) return AGError(2,@"pid 无效");
        if(AGChildFinished(pid))return AGJSON(@{@"ok":@YES,@"killed":@YES,@"forced":@NO});
        if (kill(pid, SIGTERM) != 0 && errno != ESRCH) {
            return AGError(3,[NSString stringWithFormat:@"发送终止信号失败(errno=%d)", errno]);
        }
        // 给 300ms 收尾时间；还活着就强杀。
        for (int i = 0; i < 30; i++) {
            if (AGChildFinished(pid) || (kill(pid, 0) != 0 && errno==ESRCH)) return AGJSON(@{@"ok":@YES, @"killed":@YES, @"forced":@NO});
            usleep(10 * 1000);
        }
        if(kill(pid,SIGKILL)!=0 && errno!=ESRCH)return AGError(4,@"强制终止失败，需要核对子进程状态");
        for(int i=0;i<100;i++){
            if(AGChildFinished(pid)||(kill(pid,0)!=0&&errno==ESRCH))return AGJSON(@{@"ok":@YES,@"killed":@YES,@"forced":@YES});
            usleep(10*1000);
        }
        return AGError(4,@"已发送终止信号，尚未确认子进程退出");
    }
    if ([op isEqualToString:@"utils.alive"]) {
        int pid = [args[@"pid"] intValue];
        if (pid <= 0) return AGJSON(@{@"alive":@NO});
        BOOL alive = !AGChildFinished(pid) && ((kill(pid, 0) == 0) || (errno == EPERM));
        return AGJSON(@{@"alive":@(alive)});
    }

    if ([op isEqualToString:@"utils.exec"]) {
        NSString *path = args[@"path"];
        if (path.length == 0) return AGError(2,@"缺少可执行文件路径");
        NSArray *extra = args[@"args"];
        NSMutableArray *argvList = [NSMutableArray arrayWithObject:path];
        if ([extra isKindOfClass:[NSArray class]]) {
            for (id item in extra) [argvList addObject:[item description]];
        }
        char **argv = calloc(argvList.count + 1, sizeof(char *));
        for (NSUInteger i = 0; i < argvList.count; i++) argv[i] = strdup([argvList[i] UTF8String]);
        pid_t child = 0;
        int rc = posix_spawn(&child, path.UTF8String, NULL, NULL, argv, environ);
        for (NSUInteger i = 0; i < argvList.count; i++) free(argv[i]);
        free(argv);
        if (rc != 0) return AGError(3,@"系统拒绝启动进程（需要 platform-application 权限）");
        int status = 0;
        pid_t waited;do{waited=waitpid(child,&status,0);}while(waited<0&&errno==EINTR);
        if(waited<0)return AGError(3,@"等待子进程失败");
        int code = WIFEXITED(status) ? WEXITSTATUS(status) : -1;
        return AGJSON(@{@"code":@(code)});
    }

    return AGError(1,@"未知工具操作");
}
