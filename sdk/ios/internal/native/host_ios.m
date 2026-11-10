//go:build ios && cgo

// 进程与系统工具：按名查 pid、读进程内存/CPU、自重启，以及 Toast/Alert/执行外部程序。
//
// 两条硬约束写在这里，免得后来人踩：
//   1. 弹窗类（Toast/Alert/InputAlert）必须在主线程，而且 Alert 要等用户点按钮 ——
//      不能用 AGOnMain（它有 3 秒上限，会误判超时），这里用信号量自己等。
//   2. 启动外部进程（ExecBinary / RestartSelf）在普通签名下会被系统拒绝，
//      和 platform-application 权限绑死；失败必须如实返回 -1，不能假装成功。
#import "runtime.h"
#include <sys/sysctl.h>
#include <sys/proc_info.h>
#include <libproc.h>
#include <spawn.h>
#include <signal.h>
#include <mach-o/dyld.h>
#include <unistd.h>
#include <stdlib.h>

extern char **environ;

// AGFindPID 遍历所有进程，按可执行名匹配；找不到返回 0。
static int AGFindPID(NSString *name) {
    if (name.length == 0) return 0;
    int mib[4] = {CTL_KERN, KERN_PROC, KERN_PROC_ALL, 0};
    size_t size = 0;
    if (sysctl(mib, 4, NULL, &size, NULL, 0) != 0 || size == 0) return 0;
    struct kinfo_proc *procs = malloc(size);
    if (!procs) return 0;
    int found = 0;
    if (sysctl(mib, 4, procs, &size, NULL, 0) == 0) {
        int count = (int)(size / sizeof(struct kinfo_proc));
        for (int i = 0; i < count; i++) {
            const char *candidate = procs[i].kp_proc.p_comm;
            if (strcmp(candidate, name.UTF8String) == 0) { found = procs[i].kp_proc.p_pid; break; }
        }
    }
    free(procs);
    return found;
}

ag_ios_result AGSystem(NSString *op, NSDictionary *args) {
    if ([op isEqualToString:@"system.pid"]) {
        return AGJSON(@{@"pid":@(AGFindPID(args[@"name"]))});
    }
    if ([op isEqualToString:@"system.memory"]) {
        int pid = [args[@"pid"] intValue];
        if (pid <= 0) return AGError(2,@"pid 无效");
        struct proc_taskinfo info;
        int size = proc_pidinfo(pid, PROC_PIDTASKINFO, 0, &info, sizeof(info));
        if (size <= 0) return AGError(3,@"读不到该进程信息");
        return AGJSON(@{@"kb":@((long long)info.pti_resident_size / 1024)});
    }
    if ([op isEqualToString:@"system.cpu"]) {
        int pid = [args[@"pid"] intValue];
        if (pid <= 0) return AGError(2,@"pid 无效");
        struct rusage_info_v2 a, b;
        if (proc_pid_rusage(pid, RUSAGE_INFO_V2, (rusage_info_t *)&a) != 0) return AGError(3,@"读不到 CPU 信息");
        usleep(200 * 1000);
        if (proc_pid_rusage(pid, RUSAGE_INFO_V2, (rusage_info_t *)&b) != 0) return AGError(3,@"读不到 CPU 信息");
        // 单位是 mach absolute time，换算成 200ms 内的占比。
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
        if (posix_spawn(&child, path, NULL, NULL, argv, environ) != 0) return AGError(3,@"重启失败：系统拒绝启动进程");
        // 新实例已经拉起，旧进程退出前给一点时间让句柄交接。
        usleep(200 * 1000);
        exit(0);
        return AGJSON(@{@"submitted":@YES});
    }
    return AGError(1,@"未知系统操作");
}

// ---- 弹窗 ----
//
// AGRunOnMainAndWait 把 block 丢到主线程执行并等到完成（不设短上限：
// Alert 要等人点按钮，用 AGOnMain 的 3 秒会直接判超时）。
static void AGRunOnMainAndWait(dispatch_block_t block) {
    if ([NSThread isMainThread]) { block(); return; }
    dispatch_semaphore_t done = dispatch_semaphore_create(0);
    dispatch_async(dispatch_get_main_queue(), ^{ block(); dispatch_semaphore_signal(done); });
    dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);
    dispatch_release(done);
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
        BOOL wantsInput = [op isEqualToString:@"utils.input_alert"];
        NSArray *buttons = args[@"buttons"];
        NSString *btn1 = [buttons isKindOfClass:[NSArray class]] && buttons.count > 0 ? buttons[0] : @"确定";
        NSString *btn2 = [buttons isKindOfClass:[NSArray class]] && buttons.count > 1 ? buttons[1] : nil;
        __block NSInteger index = -1;
        __block NSString *text = @"";
        __block BOOL confirmed = NO;
        AGRunOnMainAndWait(^{
            UIViewController *root = UIApplication.sharedApplication.keyWindow.rootViewController;
            if (!root) return;
            UIAlertController *alert = [UIAlertController alertControllerWithTitle:args[@"title"] ?: @""
                                                                          message:args[@"content"] ?: @""
                                                                   preferredStyle:UIAlertControllerStyleAlert];
            if (wantsInput) {
                [alert addTextFieldWithConfigurationHandler:^(UITextField *field) {
                    field.placeholder = args[@"placeholder"] ?: @"";
                    field.text = args[@"default"] ?: @"";
                }];
            }
            [alert addAction:[UIAlertAction actionWithTitle:btn1 style:UIAlertActionStyleDefault handler:^(UIAlertAction *a) {
                index = 0; confirmed = YES;
                if (wantsInput) text = alert.textFields.firstObject.text ?: @"";
            }]];
            if (btn2.length) {
                [alert addAction:[UIAlertAction actionWithTitle:btn2 style:UIAlertActionStyleCancel handler:^(UIAlertAction *a) {
                    index = 1; confirmed = NO;
                    if (wantsInput) text = alert.textFields.firstObject.text ?: @"";
                }]];
            }
            [root presentViewController:alert animated:YES completion:nil];
        });
        if (wantsInput) return AGJSON(@{@"text":text ?: @"",@"confirm":@(confirmed),@"index":@(index)});
        return AGJSON(@{@"index":@(index)});
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
        waitpid(child, &status, 0);
        int code = WIFEXITED(status) ? WEXITSTATUS(status) : -1;
        return AGJSON(@{@"code":@(code)});
    }

    return AGError(1,@"未知工具操作");
}
