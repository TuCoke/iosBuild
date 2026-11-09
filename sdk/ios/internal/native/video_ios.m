//go:build ios && cgo

#import "runtime.h"
#import <VideoToolbox/VideoToolbox.h>
#import <CoreVideo/CoreVideo.h>
#import <CoreMedia/CoreMedia.h>
#include <time.h>

@interface AGEncoder : NSObject {
@public uint64_t token;
    int nativeW,nativeH,width,height,rotation,fps;
    size_t byteLimit,queuedBytes;
    VTCompressionSessionRef session;
    dispatch_queue_t queue;
    dispatch_source_t timer;
    NSCondition *condition;
    NSMutableArray *packets;
    NSString *failure;
    BOOL stopped,needKey,forceKey;
    int pending;
    uint64_t startedAt;
}
- (ag_ios_result)prepare:(NSDictionary *)config display:(NSDictionary *)display;
- (void)tick;
- (void)fail:(NSString *)reason;
- (void)shutdown;
- (void)stop;
@end

static AGEncoder *active;
static BOOL stopping;
static const uint8_t startCode[4] = {0,0,0,1};

static void Encoded(void *ref,void *frame,OSStatus status,VTEncodeInfoFlags flags,CMSampleBufferRef sample) {
    AGEncoder *encoder = (AGEncoder *)ref;
    @autoreleasepool {
        [encoder->condition lock]; encoder->pending = MAX(0,encoder->pending-1); BOOL stopped = encoder->stopped; [encoder->condition unlock];
        if (stopped) return;
        if (status) { [encoder fail:[NSString stringWithFormat:@"视频编码回调失败: %d",(int)status]]; return; }
        if (!sample || !CMSampleBufferDataIsReady(sample) || (flags & kVTEncodeInfo_FrameDropped)) return;
        CFArrayRef attachments = CMSampleBufferGetSampleAttachmentsArray(sample,false);
        BOOL key = YES;
        if (attachments && CFArrayGetCount(attachments)) {
            CFDictionaryRef item = CFArrayGetValueAtIndex(attachments,0);
            key = !CFDictionaryContainsKey(item,kCMSampleAttachmentKey_NotSync) || !CFBooleanGetValue(CFDictionaryGetValue(item,kCMSampleAttachmentKey_NotSync));
        }
        CMFormatDescriptionRef description = CMSampleBufferGetFormatDescription(sample);
        if (!description) { [encoder fail:@"视频格式描述缺失"]; return; }
        size_t count = 0,paramSize = 0; const uint8_t *param = NULL; int lengthBytes = 0;
        OSStatus parameterStatus = CMVideoFormatDescriptionGetH264ParameterSetAtIndex(description,0,&param,&paramSize,&count,&lengthBytes);
        if (parameterStatus || (lengthBytes != 1 && lengthBytes != 2 && lengthBytes != 4)) { [encoder fail:@"H.264 参数集或 NAL 长度字段无效"]; return; }
        NSMutableData *output = [NSMutableData data];
        if (key) {
            for (size_t i = 0; i < count; i++) {
                if (CMVideoFormatDescriptionGetH264ParameterSetAtIndex(description,i,&param,&paramSize,NULL,NULL) != noErr) { [encoder fail:@"读取 H.264 参数集失败"]; return; }
                [output appendBytes:startCode length:4]; [output appendBytes:param length:paramSize];
            }
        }
        CMBlockBufferRef block = CMSampleBufferGetDataBuffer(sample);
        size_t length = block ? CMBlockBufferGetDataLength(block) : 0;
        if (!length || length > 32*1024*1024) { [encoder fail:@"编码包为空或过大"]; return; }
        NSMutableData *avcc = [NSMutableData dataWithLength:length];
        if (CMBlockBufferCopyDataBytes(block,0,length,avcc.mutableBytes) != kCMBlockBufferNoErr) { [encoder fail:@"编码数据复制失败"]; return; }
        const uint8_t *bytes = avcc.bytes; size_t offset = 0;
        while (offset < length) {
            if (length-offset < lengthBytes) { [encoder fail:@"H.264 包长度截断"]; return; }
            uint32_t nal = 0; for (int i = 0; i < lengthBytes; i++) nal = (nal<<8)|bytes[offset++];
            if (!nal || nal > length-offset) { [encoder fail:@"H.264 NAL 长度越界"]; return; }
            [output appendBytes:startCode length:4]; [output appendBytes:bytes+offset length:nal]; offset += nal;
        }
        CMTime pts = CMSampleBufferGetPresentationTimeStamp(sample);
        if (!CMTIME_IS_NUMERIC(pts)) { [encoder fail:@"视频时间戳无效"]; return; }
        int64_t ptsUS = CMTimeConvertScale(pts,1000000,kCMTimeRoundingMethod_Default).value;
        BOOL oversized = NO;
        [encoder->condition lock];
        if (!encoder->stopped) {
            if (output.length > encoder->byteLimit) oversized = YES;
            else {
                if (encoder->queuedBytes+output.length > encoder->byteLimit || encoder->packets.count >= 120) {
                    // 不从 GOP 中随意丢一帧继续发送 P 帧；清空积压，等带 SPS/PPS 的 IDR 恢复。
                    [encoder->packets removeAllObjects]; encoder->queuedBytes = 0;
                    encoder->needKey = YES; encoder->forceKey = YES;
                }
                if (key || !encoder->needKey) {
                    if (key) encoder->needKey = NO;
                    [encoder->packets addObject:@{@"data":output,@"pts":@(ptsUS),@"key":@(key)}];
                    encoder->queuedBytes += output.length;
                    [encoder->condition broadcast];
                }
            }
        }
        [encoder->condition unlock];
        if (oversized) [encoder fail:@"单个关键帧超过视频缓冲上限，请提高缓冲或降低画质"];
    }
}

@implementation AGEncoder
- (id)init {
    if ((self = [super init])) { condition = [NSCondition new]; packets = [NSMutableArray new]; needKey = YES; forceKey = YES; queue = dispatch_queue_create("autogov2.ios.video",DISPATCH_QUEUE_SERIAL); }
    return self;
}
- (void)dealloc {
    if (session) { VTCompressionSessionInvalidate(session); CFRelease(session); }
    if (queue) dispatch_release(queue);
    [condition release]; [packets release]; [failure release]; [super dealloc];
}
- (ag_ios_result)prepare:(NSDictionary *)config display:(NSDictionary *)display {
    nativeW = [display[@"width"] intValue]; nativeH = [display[@"height"] intValue];
    rotation = [config[@"rotation"] intValue]; fps = [config[@"fps"] intValue]; byteLimit = [config[@"queueBytes"] unsignedLongLongValue];
    int maxSize = [config[@"maxSize"] intValue], bitrate = [config[@"bitrate"] intValue];
    if (nativeW<=0 || nativeH<=0 || nativeW>8192 || nativeH>8192 || (uint64_t)nativeW*nativeH*4>64*1024*1024 || maxSize<64 || maxSize>4096 || fps<1 || fps>60 || rotation<0 || rotation>3 || byteLimit<256*1024 || byteLimit>32*1024*1024 || bitrate<64000 || bitrate>50000000) return AGError(2,@"视频参数或屏幕尺寸无效");
    width = rotation%2 ? nativeH : nativeW; height = rotation%2 ? nativeW : nativeH;
    double factor = MIN(1.0,(double)maxSize/MAX(width,height)); width = MAX(2,((int)(width*factor))&~1); height = MAX(2,((int)(height*factor))&~1);
    NSDictionary *pixel = @{(id)kCVPixelBufferPixelFormatTypeKey:@(kCVPixelFormatType_32BGRA),(id)kCVPixelBufferWidthKey:@(width),(id)kCVPixelBufferHeightKey:@(height),(id)kCVPixelBufferCGImageCompatibilityKey:@YES,(id)kCVPixelBufferCGBitmapContextCompatibilityKey:@YES};
    OSStatus status = VTCompressionSessionCreate(kCFAllocatorDefault,width,height,kCMVideoCodecType_H264,NULL,(CFDictionaryRef)pixel,NULL,Encoded,self,&session);
    if (status) return AGError(3,[NSString stringWithFormat:@"无法创建编码器: %d",(int)status]);
    NSDictionary *properties = @{(id)kVTCompressionPropertyKey_RealTime:@YES,(id)kVTCompressionPropertyKey_AllowFrameReordering:@NO,(id)kVTCompressionPropertyKey_ProfileLevel:(id)kVTProfileLevel_H264_Baseline_AutoLevel,(id)kVTCompressionPropertyKey_AverageBitRate:@(bitrate),(id)kVTCompressionPropertyKey_ExpectedFrameRate:@(fps),(id)kVTCompressionPropertyKey_MaxKeyFrameInterval:@(fps*2)};
    status = VTSessionSetProperties(session,(CFDictionaryRef)properties);
    if (status) return AGError(3,[NSString stringWithFormat:@"编码参数被拒绝: %d",(int)status]);
    status = VTCompressionSessionPrepareToEncodeFrames(session);
    if (status) return AGError(3,[NSString stringWithFormat:@"编码器准备失败: %d",(int)status]);
    startedAt = clock_gettime_nsec_np(CLOCK_UPTIME_RAW);
    timer = dispatch_source_create(DISPATCH_SOURCE_TYPE_TIMER,0,0,queue);
    if (!timer) return AGError(3,@"无法创建视频采集定时器");
    dispatch_source_set_timer(timer,dispatch_time(DISPATCH_TIME_NOW,0),NSEC_PER_SEC/fps,NSEC_PER_MSEC);
    dispatch_source_set_event_handler(timer,^{ @autoreleasepool { [self tick]; } });
    dispatch_resume(timer);
    return AGJSON(@{@"width":@(width),@"height":@(height)});
}
- (void)tick {
    [condition lock]; BOOL skip = stopped || failure != nil || pending >= 2; BOOL force = forceKey; if (!skip) { forceKey = NO; pending++; } [condition unlock];
    if (skip) return;
    NSString *error = nil; CGImageRef image = AGCopyScreenImage(nativeW,nativeH,&error);
    if (!image) { [condition lock]; pending--; [condition unlock]; [self fail:error]; return; }
    CVPixelBufferRef pixel = NULL;
    CVPixelBufferPoolRef pool = VTCompressionSessionGetPixelBufferPool(session);
    CVReturn cv = pool ? CVPixelBufferPoolCreatePixelBuffer(kCFAllocatorDefault,pool,&pixel) : kCVReturnError;
    if (cv != kCVReturnSuccess || !pixel) { CGImageRelease(image); [condition lock]; pending--; [condition unlock]; [self fail:@"视频像素缓冲分配失败"]; return; }
    cv = CVPixelBufferLockBaseAddress(pixel,0);
    if (cv != kCVReturnSuccess) { CGImageRelease(image); CVPixelBufferRelease(pixel); [condition lock]; pending--; [condition unlock]; [self fail:@"视频像素缓冲锁定失败"]; return; }
    CGColorSpaceRef color = CGColorSpaceCreateDeviceRGB();
    CGContextRef context = CGBitmapContextCreate(CVPixelBufferGetBaseAddress(pixel),width,height,8,CVPixelBufferGetBytesPerRow(pixel),color,kCGImageAlphaPremultipliedFirst|kCGBitmapByteOrder32Little); CGColorSpaceRelease(color);
    if (context) { AGDrawImage(context,image,width,height,rotation); CGContextRelease(context); }
    CGImageRelease(image); CVPixelBufferUnlockBaseAddress(pixel,0);
    if (!context) { CVPixelBufferRelease(pixel); [condition lock]; pending--; [condition unlock]; [self fail:@"视频画面转换失败"]; return; }
    int64_t elapsedUS = (int64_t)((clock_gettime_nsec_np(CLOCK_UPTIME_RAW)-startedAt)/1000);
    NSDictionary *options = force ? @{(id)kVTEncodeFrameOptionKey_ForceKeyFrame:@YES} : nil;
    OSStatus status = VTCompressionSessionEncodeFrame(session,pixel,CMTimeMake(elapsedUS,1000000),CMTimeMake(1,fps),(CFDictionaryRef)options,NULL,NULL);
    CVPixelBufferRelease(pixel);
    if (status) { [condition lock]; pending = MAX(0,pending-1); [condition unlock]; [self fail:[NSString stringWithFormat:@"视频帧提交失败: %d",(int)status]]; }
}
- (void)fail:(NSString *)reason {
    [condition lock]; BOOL first = failure == nil && !stopped;
    if (first) failure = [(reason ?: @"视频采集失败") copy]; [condition broadcast]; [condition unlock];
    if (first) dispatch_async(queue,^{ [self shutdown]; });
}
- (void)shutdown {
    [condition lock]; stopped = YES; [packets removeAllObjects]; queuedBytes = 0; [condition broadcast]; [condition unlock];
    if (timer) { dispatch_source_cancel(timer); dispatch_source_set_event_handler(timer,NULL); dispatch_release(timer); timer = NULL; }
    if (session) { VTCompressionSessionCompleteFrames(session,kCMTimeInvalid); VTCompressionSessionInvalidate(session); CFRelease(session); session = NULL; }
    AGReleaseCaptureSurface();
}
- (void)stop { dispatch_sync(queue,^{ [self shutdown]; }); }
@end

ag_ios_result AGVideo(NSString *op,NSDictionary *args) {
    uint64_t requested = [args[@"token"] unsignedLongLongValue];
    if (!requested) return AGError(2,@"缺少视频会话标识");
    if ([op isEqualToString:@"video.start"]) {
        if (!AGSymbol("CARenderServerRenderDisplay")) return AGError(1,@"视频屏幕采集接口不可用");
        // 主线程调用发生在全局视频锁之外，避免 UIKit 任务与视频控制互相等待。
        ag_ios_result r = AGDevice(@"device.display",@{}); if (r.code) return r;
        NSDictionary *display = [NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:r.data length:r.length] options:0 error:NULL]; free(r.data);
        @synchronized ([AGEncoder class]) {
            if (active) return AGError(5,@"进程内已有视频流，请先关闭");
            AGEncoder *encoder = [AGEncoder new]; encoder->token = requested;
            r = [encoder prepare:args[@"config"] display:display];
            if (r.code) { [encoder stop]; [encoder release]; return r; }
            active = encoder; return r;
        }
    }
    AGEncoder *encoder;
    @synchronized ([AGEncoder class]) {
        if (!active || active->token != requested) return AGError(7,@"视频会话已关闭或不属于当前调用者");
        encoder = [active retain];
        if ([op isEqualToString:@"video.stop"]) {
            if (stopping) { [encoder release]; return AGError(5,@"视频正在停止"); }
            stopping = YES;
        }
    }
    ag_ios_result result = {0};
    @try {
        if ([op isEqualToString:@"video.stop"]) {
            [encoder stop];
            @synchronized ([AGEncoder class]) { if (active == encoder) { [active release]; active = nil; } stopping = NO; }
            return AGJSON(@{});
        }
        if ([op isEqualToString:@"video.read"]) {
            [encoder->condition lock];
            NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:0.1];
            while (!encoder->packets.count && !encoder->stopped && !encoder->failure && [encoder->condition waitUntilDate:deadline]) {}
            if (encoder->failure) result = AGError(3,encoder->failure);
            else if (encoder->stopped) result = AGError(7,@"视频已停止");
            else if (encoder->packets.count) {
                NSDictionary *packet = encoder->packets[0]; NSData *bytes = packet[@"data"];
                result = AGData(bytes); result.pts_us = [packet[@"pts"] longLongValue]; result.keyframe = [packet[@"key"] boolValue]; result.width = encoder->width; result.height = encoder->height;
                encoder->queuedBytes -= bytes.length; [encoder->packets removeObjectAtIndex:0];
            }
            [encoder->condition unlock]; return result;
        }
        if ([op isEqualToString:@"video.keyframe"]) {
            [encoder->condition lock]; encoder->forceKey = YES; BOOL stopped = encoder->stopped; [encoder->condition unlock];
            return stopped ? AGError(7,@"视频已停止") : AGJSON(@{});
        }
        if ([op isEqualToString:@"video.bitrate"]) {
            int bitrate = [args[@"bitrate"] intValue]; if (bitrate<64000 || bitrate>50000000) return AGError(2,@"码率越界");
            __block OSStatus status = -1;
            dispatch_sync(encoder->queue,^{ if (encoder->session) status = VTSessionSetProperty(encoder->session,kVTCompressionPropertyKey_AverageBitRate,(CFNumberRef)@(bitrate)); });
            return status ? AGError(3,[NSString stringWithFormat:@"调整码率失败: %d",(int)status]) : AGJSON(@{});
        }
        return AGError(1,@"未知视频操作");
    } @finally {
        // 停止过程中发生异常时保留实例供原句柄重试，不能永久卡住 stopping 标记。
        if ([op isEqualToString:@"video.stop"]) {
            @synchronized ([AGEncoder class]) { if (active == encoder) stopping = NO; }
        }
        [encoder release];
    }
}
