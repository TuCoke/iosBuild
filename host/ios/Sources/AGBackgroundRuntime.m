#import "AGBackgroundRuntime.h"
#import "AGAppCtlServer.h"
#import <UIKit/UIKit.h>
#import <AVFoundation/AVFoundation.h>
#include <fcntl.h>
#include <unistd.h>
#include <stdint.h>
#include <string.h>

static void AGWriteLE32(uint8_t *p,uint32_t n) { for(int i=0;i<4;i++)p[i]=(uint8_t)(n>>(8*i)); }
static NSData *AGSilentWave(void) {
    // 自行构造一秒 8 kHz、16 bit 单声道静音 PCM，不加载参考样本文件。
    NSMutableData *data=[NSMutableData dataWithLength:44+16000];uint8_t *p=data.mutableBytes;
    memcpy(p,"RIFF",4);AGWriteLE32(p+4,16036);memcpy(p+8,"WAVEfmt ",8);
    AGWriteLE32(p+16,16);p[20]=1;p[22]=1;AGWriteLE32(p+24,8000);
    AGWriteLE32(p+28,16000);p[32]=2;p[34]=16;memcpy(p+36,"data",4);AGWriteLE32(p+40,16000);
    return data;
}

@interface AGBackgroundRuntime () <AVAudioPlayerDelegate>
@property(nonatomic,strong) NSMutableSet<NSString *> *owners;
@property(nonatomic,strong) AVAudioPlayer *player;
@property(nonatomic) BOOL enabled;
@property(nonatomic) BOOL interrupted;
@property(nonatomic) BOOL audioPlaying;
@property(nonatomic) BOOL sessionActive;
@property(nonatomic,copy) NSString *appState;
@property(nonatomic,copy) NSString *lastError;
@property(nonatomic,copy) NSString *lastEvent;
@property(nonatomic,strong) NSMutableArray *observers;
@property(nonatomic,strong) dispatch_queue_t lifecycleQueue;
@end

@implementation AGBackgroundRuntime
+ (instancetype)shared {
    static AGBackgroundRuntime *value;static dispatch_once_t once;
    dispatch_once(&once,^{value=[AGBackgroundRuntime new];value.owners=[NSMutableSet new];value.observers=[NSMutableArray new];value.enabled=YES;value.appState=@"starting";value.lifecycleQueue=dispatch_queue_create("cc.agov.background.lifecycle",DISPATCH_QUEUE_SERIAL);});
    return value;
}
- (void)record:(NSString *)event {
    @synchronized(self){self.lastEvent=[NSISO8601DateFormatter.new stringFromDate:NSDate.date];}
    NSString *line=[NSString stringWithFormat:@"[background] %@ %@\n",self.lastEvent,event];
    NSLog(@"%@",line);
    NSString *docs=NSSearchPathForDirectoriesInDomains(NSDocumentDirectory,NSUserDomainMask,YES).firstObject;
    if(!docs.length)return;
    NSString *path=[docs stringByAppendingPathComponent:@"app.log"];
    // 单次 append 写入，不截断日志，不记录令牌或脚本正文。
    int flags=O_WRONLY|O_CREAT|O_APPEND;
#ifdef O_NOFOLLOW
    flags|=O_NOFOLLOW;
#endif
    int fd=open(path.fileSystemRepresentation,flags,0600);
    if(fd>=0){NSData *bytes=[line dataUsingEncoding:NSUTF8StringEncoding];(void)write(fd,bytes.bytes,bytes.length);close(fd);}
}
- (NSDictionary *)snapshot {
    @synchronized(self){return @{@"available":@YES,@"enabled":@(self.enabled),@"requested":@(self.owners.count>0),@"owners":[self.owners.allObjects sortedArrayUsingSelector:@selector(compare:)],@"audioPlaying":@(self.audioPlaying),@"audioSessionActive":@(self.sessionActive),@"interrupted":@(self.interrupted),@"appState":self.appState ?: @"unknown",@"lastError":self.lastError ?: @"",@"lastEventAt":self.lastEvent ?: @"",@"guaranteedBackgroundExecution":@NO};}
}
- (void)reconcile {
    NSAssert(NSThread.isMainThread,@"后台音频必须在主线程管理");
    @synchronized(self){
        BOOL desired=self.enabled && self.owners.count>0 && !self.interrupted;
        AVAudioSession *session=AVAudioSession.sharedInstance;
        if(!desired){
            BOOL wasActive=self.sessionActive || self.audioPlaying;
            [self.player stop];self.player=nil;self.audioPlaying=NO;
            NSError *error=nil;
            if(self.sessionActive && ![session setActive:NO withOptions:AVAudioSessionSetActiveOptionNotifyOthersOnDeactivation error:&error])self.lastError=error.localizedDescription;
            self.sessionActive=error!=nil;
            if(wasActive)[self record:@"静音音频已停止；无运行需求、已禁用或发生音频中断"];
            return;
        }
        if(self.player.isPlaying){self.audioPlaying=YES;return;}
        NSError *error=nil;
        if(![session setCategory:AVAudioSessionCategoryPlayback mode:AVAudioSessionModeDefault options:AVAudioSessionCategoryOptionMixWithOthers error:&error] || ![session setActive:YES error:&error]){
            self.lastError=error.localizedDescription ?: @"音频会话激活失败";self.audioPlaying=NO;[self record:self.lastError];return;
        }
        self.sessionActive=YES;
        self.player=[[AVAudioPlayer alloc] initWithData:AGSilentWave() error:&error];
        self.player.delegate=self;self.player.numberOfLoops=-1;self.player.volume=0;
        if(!self.player || ![self.player prepareToPlay] || ![self.player play]){
            self.lastError=error.localizedDescription ?: @"静音播放器未成功运行";self.audioPlaying=NO;
            [self.player stop];self.player=nil;NSError *deactivateError=nil;
            self.sessionActive=![session setActive:NO withOptions:AVAudioSessionSetActiveOptionNotifyOthersOnDeactivation error:&deactivateError];
            [self record:self.lastError];return;
        }
        self.audioPlaying=YES;self.lastError=@"";[self record:@"静音音频已运行（混音、无限循环、音量 0）；这不是永久后台保证"];
    }
}
- (void)stateChanged {
    UIApplicationState state=UIApplication.sharedApplication.applicationState;
    @synchronized(self){self.appState=state==UIApplicationStateActive?@"active":state==UIApplicationStateBackground?@"background":@"inactive";
        if(state==UIApplicationStateActive)self.interrupted=NO;}
    [self record:[@"宿主状态：" stringByAppendingString:self.appState]];[self reconcile];
    if(state==UIApplicationStateActive || state==UIApplicationStateBackground){
        NSString *event=state==UIApplicationStateActive?@"resume":@"pause";
        // JS 客户端可能慢，串行队列保序，不能在 UIKit 主线程等待 socket 写入。
        dispatch_async(self.lifecycleQueue,^{[[AGAppCtlServer shared] emitLifecycle:event];});
    }
}
- (void)install {
    NSAssert(NSThread.isMainThread,@"生命周期观察器必须在主线程安装");
    if(self.observers.count)return;
    NSNotificationCenter *center=NSNotificationCenter.defaultCenter;
    __weak AGBackgroundRuntime *weakSelf=self;
    for(NSString *name in @[UIApplicationDidEnterBackgroundNotification,UIApplicationWillEnterForegroundNotification,UIApplicationDidBecomeActiveNotification,UIApplicationWillResignActiveNotification]){
        [self.observers addObject:[center addObserverForName:name object:nil queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *note){[weakSelf stateChanged];}]];
    }
    [self.observers addObject:[center addObserverForName:AVAudioSessionInterruptionNotification object:nil queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *note){
        AGBackgroundRuntime *strong=weakSelf;if(!strong)return;
        BOOL began=[note.userInfo[AVAudioSessionInterruptionTypeKey] unsignedIntegerValue]==AVAudioSessionInterruptionTypeBegan;
        BOOL resume=([note.userInfo[AVAudioSessionInterruptionOptionKey] unsignedIntegerValue]&AVAudioSessionInterruptionOptionShouldResume)!=0;
        @synchronized(strong){strong.interrupted=began || (!resume && UIApplication.sharedApplication.applicationState!=UIApplicationStateActive);strong.audioPlaying=NO;}
        [strong record:began?@"音频中断开始":resume?@"音频中断结束，允许恢复":@"音频中断结束，后台恢复需系统许可"];
        [strong reconcile];
    }]];
    [self.observers addObject:[center addObserverForName:AVAudioSessionMediaServicesWereResetNotification object:nil queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *note){
        AGBackgroundRuntime *strong=weakSelf;if(!strong)return;
        @synchronized(strong){strong.player=nil;strong.sessionActive=NO;strong.audioPlaying=NO;strong.interrupted=NO;}
        [strong record:@"系统音频服务重置，重新建立播放器"];[strong reconcile];
    }]];
    [self stateChanged];
}
- (NSDictionary *)command:(NSString *)command owner:(NSString *)owner enabled:(BOOL)enabled {
    BOOL added=NO,changed=NO;
    @synchronized(self){
        if([command isEqualToString:@"acquire"] && owner.length && owner.length<=128){added=![self.owners containsObject:owner];[self.owners addObject:owner];changed=added;}
        else if([command isEqualToString:@"release"]){changed=[self.owners containsObject:owner];[self.owners removeObject:owner];}
        else if([command isEqualToString:@"enable"]){self.enabled=enabled;if(enabled)self.interrupted=NO;changed=YES;}
        else if([command isEqualToString:@"terminate"]){[self.owners removeAllObjects];self.enabled=NO;changed=YES;}
    }
    if(changed){
        [self record:[NSString stringWithFormat:@"运行需求 %@ owner=%@",command,owner]];
        // 需求先同步登记，UIKit 音频操作排到主线程；返回状态不伪称音频已生效。
        if(NSThread.isMainThread)[self reconcile];else dispatch_async(dispatch_get_main_queue(),^{[self reconcile];});
    }
    NSMutableDictionary *result=[[self snapshot] mutableCopy];result[@"added"]=@(added);return result;
}
- (void)audioPlayerDecodeErrorDidOccur:(AVAudioPlayer *)player error:(NSError *)error {
    @synchronized(self){self.audioPlaying=NO;self.lastError=error.localizedDescription ?: @"静音音频解码失败";}
    [self record:self.lastError];
}
@end
