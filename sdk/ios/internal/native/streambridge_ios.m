//go:build ios && cgo

#import "runtime.h"
#include <limits.h>
#include <stdlib.h>
#include <string.h>

// C 回调与 Go 拉取复用一个 VideoToolbox 编码器，UINT64_MAX 仅保留给 C 回调会话。
static NSLock *callbackLock;
static NSRecursiveLock *controlLock;
static dispatch_queue_t h264Queue,rawQueue;
static dispatch_source_t rawTimer;
static char h264QueueKey,rawQueueKey;
static AGH264Callback h264Callback;
static AGRawCallback rawCallback;
static BOOL encoding,encoderOwned,rawRunning,needKeyframe;
static NSUInteger generation,rawGeneration,pendingCallbacks;
static NSString *lastFailure;
static int maxSize=960,bitrate=1500000,frameRate=15;
static void AGStreamInit(void){static dispatch_once_t once;dispatch_once(&once,^{callbackLock=[NSLock new];controlLock=[NSRecursiveLock new];h264Queue=dispatch_queue_create("agov.ios.h264.callbacks",DISPATCH_QUEUE_SERIAL);rawQueue=dispatch_queue_create("agov.ios.raw.callbacks",DISPATCH_QUEUE_SERIAL);dispatch_queue_set_specific(h264Queue,&h264QueueKey,&h264QueueKey,NULL);dispatch_queue_set_specific(rawQueue,&rawQueueKey,&rawQueueKey,NULL);});}
void AGStreamFailure(NSString *message){AGStreamInit();[callbackLock lock];[lastFailure release];lastFailure=[message copy];[callbackLock unlock];}
char *AGStreamFailureCopy(void){AGStreamInit();[callbackLock lock];char *out=lastFailure?strdup(lastFailure.UTF8String):NULL;[callbackLock unlock];return out;}
BOOL AGPublishH264(NSData *data,BOOL keyframe){
 AGStreamInit();[callbackLock lock];
 if(!encoding || !h264Callback || !data.length || data.length>INT_MAX){[callbackLock unlock];return NO;}
 if(pendingCallbacks>=2 || (needKeyframe && !keyframe)){needKeyframe=YES;[callbackLock unlock];return NO;}
 needKeyframe=NO;NSUInteger current=generation;pendingCallbacks++;NSData *owned=[data copy];[callbackLock unlock];
 dispatch_async(h264Queue,^{@autoreleasepool{
   [callbackLock lock];AGH264Callback cb=(encoding && current==generation)?h264Callback:NULL;[callbackLock unlock];
   if(cb)cb(owned.bytes,(int)owned.length);
   [owned release];[callbackLock lock];pendingCallbacks--;[callbackLock unlock];
 }});return YES;
}
int AGH264CallbackSet(AGH264Callback cb){AGStreamInit();[callbackLock lock];if(encoding){[callbackLock unlock];return -1;}h264Callback=cb;[callbackLock unlock];return 0;}
int AGH264ParamsSet(int resolution,int bps,int fps){
 if(resolution<64 || resolution>4096 || bps<64000 || bps>50000000 || fps<1 || fps>60)return -1;
 AGStreamInit();[callbackLock lock];if(encoding){[callbackLock unlock];return -1;}maxSize=resolution;bitrate=bps;frameRate=fps;[callbackLock unlock];return 0;
}
int AGH264Start(void){
 AGStreamInit();[controlLock lock];[callbackLock lock];if(encoderOwned || !h264Callback){[callbackLock unlock];[controlLock unlock];return -1;}
 NSDictionary *config=@{@"maxSize":@(maxSize),@"bitrate":@(bitrate),@"fps":@(frameRate),@"rotation":@0,@"autoRotation":@YES,@"queueBytes":@(4<<20)};
 [lastFailure release];lastFailure=nil;generation++;encoding=YES;encoderOwned=YES;needKeyframe=YES;[callbackLock unlock];
 ag_ios_result r=AGVideo(@"video.start",@{@"token":@(UINT64_MAX),@"config":config});int code=r.code;free(r.data);
 if(code){[callbackLock lock];encoding=NO;encoderOwned=(code==4);generation++;[callbackLock unlock];}
 [controlLock unlock];return code?-1:0;
}
int AGH264Stop(void){
 AGStreamInit();[controlLock lock];[callbackLock lock];BOOL running=encoderOwned;encoding=NO;generation++;[callbackLock unlock];
 int code=0;if(running){ag_ios_result r=AGVideo(@"video.stop",@{@"token":@(UINT64_MAX)});code=r.code;free(r.data);if(code==0 || code==7){[callbackLock lock];encoderOwned=NO;[callbackLock unlock];}}
 [controlLock unlock];
 // 不在回调线程等待自己；其他调用者等待已经开始的回调结束。
 if(!dispatch_get_specific(&h264QueueKey))dispatch_sync(h264Queue,^{});
 return code && code!=7 ? -1:0;
}
int AGRawCallbackSet(AGRawCallback cb){AGStreamInit();[callbackLock lock];if(rawRunning){[callbackLock unlock];return -1;}rawCallback=cb;[callbackLock unlock];return 0;}
int AGRawStart(void){
 AGStreamInit();[controlLock lock];
 ag_ios_result r=AGDisplayInfo();if(r.code){free(r.data);[controlLock unlock];return -1;}
 NSDictionary *display=[NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:r.data length:r.length] options:0 error:NULL];free(r.data);
 int w=[display[@"width"] intValue],h=[display[@"height"] intValue];if(w<=0 || h<=0 || (uint64_t)w*h*4>64<<20){[controlLock unlock];return -1;}
 NSString *initialError=nil;CGImageRef initial=AGCopyScreenImage(w,h,&initialError);if(!initial){[controlLock unlock];return -1;}CGImageRelease(initial);
 [callbackLock lock];if(rawRunning || !rawCallback){[callbackLock unlock];[controlLock unlock];return -1;}
 rawTimer=dispatch_source_create(DISPATCH_SOURCE_TYPE_TIMER,0,0,rawQueue);if(!rawTimer){[callbackLock unlock];[controlLock unlock];return -1;}
 [lastFailure release];lastFailure=nil;rawRunning=YES;NSUInteger current=++rawGeneration;[callbackLock unlock];
 dispatch_source_set_timer(rawTimer,dispatch_time(DISPATCH_TIME_NOW,0),NSEC_PER_SEC/30,NSEC_PER_MSEC);
 dispatch_source_set_event_handler(rawTimer,^{@autoreleasepool{
    [callbackLock lock];AGRawCallback cb=(rawRunning && current==rawGeneration)?rawCallback:NULL;[callbackLock unlock];if(!cb)return;
    NSString *failure=nil;CGImageRef image=AGCopyScreenImage(w,h,&failure);if(!image){AGStreamFailure(failure ?: @"原始帧采集失败");AGRawStop();return;}
    int turn=AGScreenRotation(),outW=(turn&1)?h:w,outH=(turn&1)?w:h;
    size_t stride=(size_t)outW*4;NSMutableData *pixels=[NSMutableData dataWithLength:stride*outH];CGColorSpaceRef color=CGColorSpaceCreateDeviceRGB();
    CGContextRef context=CGBitmapContextCreate(pixels.mutableBytes,outW,outH,8,stride,color,kCGImageAlphaPremultipliedFirst|kCGBitmapByteOrder32Little);CGColorSpaceRelease(color);
    if(context){AGDrawImage(context,image,outW,outH,turn);CGContextRelease(context);cb(pixels.bytes,outW,outH,(int)stride);}CGImageRelease(image);
 }});
 dispatch_resume(rawTimer);[controlLock unlock];return 0;
}
int AGRawStop(void){
 AGStreamInit();[controlLock lock];[callbackLock lock];rawRunning=NO;rawGeneration++;dispatch_source_t timer=rawTimer;rawTimer=NULL;[callbackLock unlock];
 if(timer){dispatch_source_cancel(timer);dispatch_source_set_event_handler(timer,NULL);dispatch_release(timer);}[controlLock unlock];
 if(!dispatch_get_specific(&rawQueueKey))dispatch_sync(rawQueue,^{});return 0;
}
