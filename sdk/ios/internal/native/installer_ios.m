//go:build ios && cgo

#import "runtime.h"
#include <spawn.h>
#include <fcntl.h>
#include <unistd.h>
#include <signal.h>
#include <errno.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <time.h>
extern char **environ;

// 只使用已授权环境提供的 persona API 启动独立子进程，不改变 Go 宿主的进程身份。
static int AGSpawnInstaller(pid_t *pid,const char *path,const posix_spawn_file_actions_t *actions,char *const argv[]){
 if(geteuid()==0)return posix_spawn(pid,path,actions,NULL,argv,environ);
 int (*persona)(posix_spawnattr_t *,uid_t,uint32_t)=AGSymbol("posix_spawnattr_set_persona_np");
 int (*uid)(posix_spawnattr_t *,uid_t)=AGSymbol("posix_spawnattr_set_persona_uid_np");
 int (*gid)(posix_spawnattr_t *,gid_t)=AGSymbol("posix_spawnattr_set_persona_gid_np");
 if(!persona || !uid || !gid)return EPERM;
 posix_spawnattr_t attributes;int rc=posix_spawnattr_init(&attributes);if(rc)return rc;
 // 常量 99 / 1 和会话标志来自参考样本的 spawn 调用点；系统仍会检查 entitlement。
 rc=posix_spawnattr_setflags(&attributes,0x400);if(!rc)rc=persona(&attributes,99,1);if(!rc)rc=uid(&attributes,0);if(!rc)rc=gid(&attributes,0);
 if(!rc)rc=posix_spawn(pid,path,actions,&attributes,argv,environ);posix_spawnattr_destroy(&attributes);return rc;
}
static int AGInstallerRun(NSString *executable,NSArray<NSString *> *arguments){
 if(NSThread.isMainThread)return -3;
 if(![[NSFileManager defaultManager] isExecutableFileAtPath:executable])return -1;
 char **argv=calloc(arguments.count+2,sizeof(char *));if(!argv)return -1;
 argv[0]=strdup(executable.fileSystemRepresentation);BOOL valid=argv[0]!=NULL;
 for(NSUInteger i=0;i<arguments.count;i++){argv[i+1]=strdup(arguments[i].UTF8String);valid=valid && argv[i+1]!=NULL;}
 posix_spawn_file_actions_t actions;int actionCode=posix_spawn_file_actions_init(&actions);BOOL actionsReady=actionCode==0;
 if(!actionCode){actionCode=posix_spawn_file_actions_addopen(&actions,STDOUT_FILENO,"/dev/null",O_WRONLY,0);if(!actionCode)actionCode=posix_spawn_file_actions_addopen(&actions,STDERR_FILENO,"/dev/null",O_WRONLY,0);}
 pid_t pid=0;int rc=!valid?ENOMEM:(actionCode?actionCode:AGSpawnInstaller(&pid,executable.fileSystemRepresentation,&actions,argv));
 if(actionsReady)posix_spawn_file_actions_destroy(&actions);
 for(NSUInteger i=0;i<arguments.count+1;i++)free(argv[i]);free(argv);if(rc)return -1;
 struct timespec start;clock_gettime(CLOCK_MONOTONIC,&start);
 for(;;){int status=0;pid_t child=waitpid(pid,&status,WNOHANG);if(child==pid)return WIFEXITED(status)?WEXITSTATUS(status):-4;if(child<0 && errno!=EINTR)return -4;
   struct timespec now;clock_gettime(CLOCK_MONOTONIC,&now);if(now.tv_sec-start.tv_sec>=60){
     kill(pid,SIGTERM);usleep(100000);
     do{child=waitpid(pid,&status,WNOHANG);}while(child<0 && errno==EINTR);
     if(child==0){kill(pid,SIGKILL);do{child=waitpid(pid,&status,0);}while(child<0 && errno==EINTR);}
     return -2;
   }usleep(20000);
 }
}
static NSString *AGTool(NSString *name){for(NSString *directory in @[@"/var/jb/usr/bin",@"/usr/bin",@"/var/jb/bin",@"/bin"]){NSString *path=[directory stringByAppendingPathComponent:name];if([[NSFileManager defaultManager] isExecutableFileAtPath:path])return path;}return nil;}
static ag_ios_result AGInstallerResult(int code,NSString *field){
 if(code==0)return AGJSON(@{field:@YES});
 if(code==-3)return AGError(5,@"安装进程不能在 UIKit 主线程等待，请从后台任务调用");
 if(code==-1)return AGError(3,@"安装工具未启动，请检查可执行文件、权限和进程资源");
 // 子进程已经启动时，非零退出也可能留下部分修改，不能误报为未执行后自动重试。
 return AGError(4,[NSString stringWithFormat:@"安装工具未确认成功(code=%d)，可能部分执行，需核对设备",code]);
}
static void AGRefreshIcons(void){NSString *tool=AGTool(@"uicache");if(tool)AGInstallerRun(tool,@[@"-a"]);}

ag_ios_result AGInstallPackage(NSString *path,BOOL *handled){
 *handled=NO;if(![path isKindOfClass:[NSString class]] || !path.length || [path rangeOfString:[NSString stringWithFormat:@"%C",(unichar)0]].location!=NSNotFound)return AGError(2,@"安装包路径无效");
 path=[[path stringByStandardizingPath] stringByResolvingSymlinksInPath];if(![[NSFileManager defaultManager] fileExistsAtPath:path]){*handled=YES;return AGError(2,@"安装包不存在");}
 NSString *extension=path.pathExtension.lowercaseString;
 if([extension isEqualToString:@"deb"]){*handled=YES;NSString *tool=AGTool(@"dpkg");if(!tool)return AGError(1,@"当前环境没有 dpkg，无法安装 deb");int code=AGInstallerRun(tool,@[@"-i",@"--",path]);if(code==0)AGRefreshIcons();return AGInstallerResult(code,@"installed");}
 if(![extension isEqualToString:@"ipa"])return (ag_ios_result){0};
 // 查找设备已安装的 TrollStore helper，不打包或执行来源不明的同名文件。
 ag_ios_result location=AGOnMain(^{Class cls=NSClassFromString(@"LSApplicationProxy");SEL get=NSSelectorFromString(@"applicationProxyForIdentifier:");if(![cls respondsToSelector:get])return AGJSON(@{});id proxy=((id(*)(id,SEL,id))objc_msgSend)(cls,get,@"com.opa334.TrollStore");SEL url=NSSelectorFromString(@"bundleURL");id bundle=[proxy respondsToSelector:url]?((id(*)(id,SEL))objc_msgSend)(proxy,url):nil;NSString *helper=[bundle isKindOfClass:[NSURL class]]?[[bundle path] stringByAppendingPathComponent:@"trollstorehelper"]:nil;return AGJSON(helper?@{@"path":helper}:@{});});
 if(location.code)return location;NSDictionary *result=[NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:location.data length:location.length] options:0 error:NULL];free(location.data);NSString *helper=result[@"path"];
 if(helper.length && [[NSFileManager defaultManager] isExecutableFileAtPath:helper]){*handled=YES;return AGInstallerResult(AGInstallerRun(helper,@[@"install",path]),@"installed");}
 return (ag_ios_result){0};
}
ag_ios_result AGUninstallPackage(NSString *identifier,BOOL *handled){
 *handled=NO;if(!identifier.length || [identifier hasPrefix:@"-"] || [identifier containsString:@"/"])return (ag_ios_result){0};
 NSString *tool=AGTool(@"dpkg");if(!tool || AGInstallerRun(tool,@[@"-s",@"--",identifier])!=0)return (ag_ios_result){0};
 *handled=YES;int code=AGInstallerRun(tool,@[@"-r",@"--",identifier]);if(code==0)AGRefreshIcons();return AGInstallerResult(code,@"uninstalled");
}
