//go:build ios && cgo

#import "runtime.h"
#include <sys/file.h>
#include <sys/stat.h>
#include <fcntl.h>
#include <unistd.h>
#include <errno.h>
#include <stdlib.h>

// 文件不删除：所有进程必须锁住同一个 inode。描述符不继承给 frpc 等子进程。
// 进程被强杀时内核自动释放锁，不能留下依赖人工删除的“锁文件状态”。
static NSLock *leaseMutex;
static int leaseFD = -1;
static NSUInteger leaseReferences;

ag_ios_result AGInputLeaseAcquire(void) {
    static dispatch_once_t once;
    dispatch_once(&once,^{leaseMutex=[NSLock new];});
    [leaseMutex lock];
    @try {
        if(leaseReferences){leaseReferences++;return AGJSON(@{});}
        NSString *directory=AGRuntimeDocumentsPath();
        BOOL isDirectory=NO;
        if(!directory.isAbsolutePath || ![NSFileManager.defaultManager fileExistsAtPath:directory isDirectory:&isDirectory] || !isDirectory)
            return AGError(3,@"设备操作锁缺少共享 Documents 目录");
        NSString *path=[directory stringByAppendingPathComponent:@".agov-device-input.lock"];
        int fd=open(path.fileSystemRepresentation,O_RDWR|O_CREAT|O_NOFOLLOW|O_CLOEXEC,0600);
        if(fd<0)return AGError(3,[NSString stringWithFormat:@"无法打开设备操作锁: %d",errno]);
        struct stat info;
        if(fstat(fd,&info)!=0 || !S_ISREG(info.st_mode)){close(fd);return AGError(3,@"设备操作锁不是普通文件");}
        int result;
        do{result=flock(fd,LOCK_EX|LOCK_NB);}while(result!=0 && errno==EINTR);
        if(result!=0){int code=errno;close(fd);return AGError(code==EWOULDBLOCK?5:3,
            code==EWOULDBLOCK?@"设备正在由其他进程执行任务，请等待任务结束":[NSString stringWithFormat:@"设备操作锁获取失败: %d",code]);}
        leaseFD=fd;leaseReferences=1;return AGJSON(@{});
    } @finally {[leaseMutex unlock];}
}
void AGInputLeaseRelease(void) {
    [leaseMutex lock];
    if(leaseReferences && --leaseReferences==0){
        // close 释放本进程的锁；不能 unlink 文件，否则另一个进程可能锁到新 inode。
        close(leaseFD);leaseFD=-1;
    }
    [leaseMutex unlock];
}
BOOL AGInputWriteRequired(NSString *op) {
    if([op hasPrefix:@"motion."])return ![op isEqualToString:@"motion.status"];
    if([op hasPrefix:@"ime."])return ![op isEqualToString:@"ime.segments"];
    static NSSet *writes;static dispatch_once_t once;
    dispatch_once(&once,^{writes=[[NSSet alloc] initWithArray:@[@"app.launch",@"app.stop",@"app.open_url",@"app.install",@"app.uninstall",@"app.clear",
        @"device.button",@"device.lock",@"device.wake",@"device.brightness",@"device.clipset",@"device.keep_awake",@"device.wifi_set",@"device.vpn_set",@"device.reboot"]];});
    return [writes containsObject:op];
}
ag_ios_result AGWithInputLease(ag_ios_result (^work)(void)) {
    ag_ios_result lease=AGInputLeaseAcquire();if(lease.code)return lease;free(lease.data);
    @try{return work();}@finally{AGInputLeaseRelease();}
}
