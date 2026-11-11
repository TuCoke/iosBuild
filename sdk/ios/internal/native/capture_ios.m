//go:build ios && cgo

#import "runtime.h"
#import <ImageIO/ImageIO.h>
#include <pthread.h>

// 【IOSurface 在 iOS 上不是公开头文件】
//
// 真机编译实测：iPhoneOS SDK 里没有 <IOSurface/IOSurface.h>（它只在 macOS SDK 里，
// iOS 侧属于私有框架）。所以这里不 import 头文件，自己在本地声明类型与函数，
// 并且全部用 dlsym 解析，避免链接期依赖私有框架（AGSymbol 在 runtime_ios.m 里）。
//
// 键名同样不能用 kIOSurfaceWidth 这些私有常量，改用同名字符串字面量：
// CFDictionary 以 CFString 的值做键比较，CFSTR("IOSurfaceWidth") 与私有常量等价。
typedef struct __IOSurface *IOSurfaceRef;

typedef IOSurfaceRef (*AGIOSurfaceCreate)(CFDictionaryRef);
typedef int (*AGIOSurfaceLock)(IOSurfaceRef, uint32_t, uint32_t *);
typedef int (*AGIOSurfaceUnlock)(IOSurfaceRef, uint32_t, uint32_t *);
typedef size_t (*AGIOSurfaceGetBytesPerRow)(IOSurfaceRef);
typedef void *(*AGIOSurfaceGetBaseAddress)(IOSurfaceRef);

enum { AGIOSurfaceLockReadOnly = 1 };

typedef int (*RenderDisplay)(uint32_t, CFStringRef, IOSurfaceRef, uint32_t, uint32_t);
static pthread_mutex_t captureMutex = PTHREAD_MUTEX_INITIALIZER;
static IOSurfaceRef surface;
static size_t surfaceW, surfaceH;

// 只在第一次使用时解析一次；任一符号缺失就整体不可用，返回 NULL，不假装成功。
static AGIOSurfaceCreate surfaceCreate;
static AGIOSurfaceLock surfaceLock;
static AGIOSurfaceUnlock surfaceUnlock;
static AGIOSurfaceGetBytesPerRow surfaceBytesPerRow;
static AGIOSurfaceGetBaseAddress surfaceBaseAddress;

static int AGScreenSurfaceReady(void) {
    static int ready;
    static dispatch_once_t once;
    // 截图、视频与原始帧可以并发进入，符号表的初始化也必须串行发布。
    dispatch_once(&once, ^{
        surfaceCreate = (AGIOSurfaceCreate)AGSymbol("IOSurfaceCreate");
        surfaceLock = (AGIOSurfaceLock)AGSymbol("IOSurfaceLock");
        surfaceUnlock = (AGIOSurfaceUnlock)AGSymbol("IOSurfaceUnlock");
        surfaceBytesPerRow = (AGIOSurfaceGetBytesPerRow)AGSymbol("IOSurfaceGetBytesPerRow");
        surfaceBaseAddress = (AGIOSurfaceGetBaseAddress)AGSymbol("IOSurfaceGetBaseAddress");
        ready = (surfaceCreate && surfaceLock && surfaceUnlock && surfaceBytesPerRow && surfaceBaseAddress) ? 1 : 0;
    });
    return ready;
}

void AGReleaseCaptureSurface(void) {
    pthread_mutex_lock(&captureMutex);
    if (surface) { CFRelease(surface); surface = NULL; }
    surfaceW = surfaceH = 0;
    pthread_mutex_unlock(&captureMutex);
}

CGImageRef AGCopyScreenImage(int width, int height, NSString **error) {
    RenderDisplay render = AGSymbol("CARenderServerRenderDisplay");
    if (!render) { *error = @"屏幕渲染接口不可用"; return NULL; }
    if (!AGScreenSurfaceReady()) { *error = @"IOSurface 接口不可用"; return NULL; }
    if (width <= 0 || height <= 0 || width > 8192 || height > 8192 || (uint64_t)width * height * 4 > 64 * 1024 * 1024) { *error = @"原生屏幕尺寸无效或超过内存限制"; return NULL; }
    pthread_mutex_lock(&captureMutex);
    CGImageRef image = NULL;
    @try {
        if (!surface || surfaceW != width || surfaceH != height) {
            if (surface) { CFRelease(surface); surface = NULL; }
            size_t stride = ((size_t)width * 4 + 63) & ~(size_t)63;
            NSDictionary *attributes = @{
                (id)CFSTR("IOSurfaceWidth"): @(width),
                (id)CFSTR("IOSurfaceHeight"): @(height),
                (id)CFSTR("IOSurfaceBytesPerElement"): @4,
                (id)CFSTR("IOSurfaceBytesPerRow"): @(stride),
                (id)CFSTR("IOSurfaceAllocSize"): @(stride * height),
                (id)CFSTR("IOSurfacePixelFormat"): @((uint32_t)0x42475241)
            };
            surface = surfaceCreate((CFDictionaryRef)attributes); surfaceW = width; surfaceH = height;
        }
        if (!surface) { *error = @"IOSurface 分配失败"; return NULL; }
        // 样本 captureScreenRaw 调用点检查 w0：0 为成功，不能再声明成 void。
        int status=render(0,CFSTR("LCD"),surface,0,0);
        if(status!=0){*error=[NSString stringWithFormat:@"屏幕渲染失败: %d",status];return NULL;}
        typedef CGImageRef (*CreateSurfaceImage)(IOSurfaceRef);
        CreateSurfaceImage createImage=(CreateSurfaceImage)AGSymbol("UICreateCGImageFromIOSurface");
        if(createImage){
            CGImageRef source=createImage(surface);
            if(source){CGColorSpaceRef color=CGColorSpaceCreateDeviceRGB();CGContextRef copy=CGBitmapContextCreate(NULL,width,height,8,0,color,kCGImageAlphaPremultipliedFirst|kCGBitmapByteOrder32Little);CGColorSpaceRelease(color);
                if(copy){CGContextDrawImage(copy,CGRectMake(0,0,width,height),source);image=CGBitmapContextCreateImage(copy);CGContextRelease(copy);}CGImageRelease(source);
                if(image)return image;
            }
        }
        if (surfaceLock(surface, AGIOSurfaceLockReadOnly, NULL) != 0) { *error = @"无法锁定截图缓冲区"; return NULL; }
        size_t stride = surfaceBytesPerRow(surface);
        void *base = surfaceBaseAddress(surface);
        if (!base || stride < (size_t)width*4 || stride > (64*1024*1024)/(size_t)height) { surfaceUnlock(surface, AGIOSurfaceLockReadOnly, NULL); *error = @"截图缓冲区地址或行长度无效"; return NULL; }
        // 复制后再解锁，图片不会引用下一帧会复用的 surface 内存。
        NSData *copy = [[NSData alloc] initWithBytes:base length:stride * height];
        surfaceUnlock(surface, AGIOSurfaceLockReadOnly, NULL);
        CGDataProviderRef provider = CGDataProviderCreateWithCFData((CFDataRef)copy);
        CGColorSpaceRef color = CGColorSpaceCreateDeviceRGB();
        image = CGImageCreate(width, height, 8, 32, stride, color, kCGImageAlphaNoneSkipFirst | kCGBitmapByteOrder32Little, provider, NULL, false, kCGRenderingIntentDefault);
        CGColorSpaceRelease(color); CGDataProviderRelease(provider); [copy release];
        if (!image) *error = @"无法创建截图图像";
    } @finally { pthread_mutex_unlock(&captureMutex); }
    return image;
}

void AGDrawImage(CGContextRef context, CGImageRef image, int width, int height, int rotation) {
    CGContextSaveGState(context);
    // 以自然方向原始帧为基准，截图和视频共用相同的方向变换。
    switch (rotation) {
        case 1: CGContextTranslateCTM(context, width, 0); CGContextRotateCTM(context, M_PI_2); CGContextDrawImage(context, CGRectMake(0, 0, height, width), image); break;
        case 2: CGContextTranslateCTM(context, width, height); CGContextRotateCTM(context, M_PI); CGContextDrawImage(context, CGRectMake(0, 0, width, height), image); break;
        case 3: CGContextTranslateCTM(context, 0, height); CGContextRotateCTM(context, -M_PI_2); CGContextDrawImage(context, CGRectMake(0, 0, height, width), image); break;
        default: CGContextDrawImage(context, CGRectMake(0, 0, width, height), image);
    }
    CGContextRestoreGState(context);
}

ag_ios_result AGCapture(NSDictionary *args) {
    ag_ios_result display = AGDevice(@"device.display", @{});
    if (display.code) return display;
    NSDictionary *info = [NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:display.data length:display.length] options:0 error:NULL]; free(display.data);
    int nativeW = [info[@"width"] intValue], nativeH = [info[@"height"] intValue];
    int rotation = [args[@"autoRotation"] boolValue] ? [info[@"rotation"] intValue] : [args[@"rotation"] intValue], maximum = [args[@"maxSize"] intValue];
    NSString *error = nil; CGImageRef source = AGCopyScreenImage(nativeW, nativeH, &error);
    if (!source) return AGError(3, error);
    int w = rotation % 2 ? nativeH : nativeW, h = rotation % 2 ? nativeW : nativeH;
    if (maximum > 0 && MAX(w, h) > maximum) { double factor = (double)maximum / MAX(w, h); w = MAX(1, (int)(w * factor)); h = MAX(1, (int)(h * factor)); }
    CGColorSpaceRef color = CGColorSpaceCreateDeviceRGB();
    CGContextRef context = CGBitmapContextCreate(NULL, w, h, 8, 0, color, kCGImageAlphaPremultipliedFirst | kCGBitmapByteOrder32Little); CGColorSpaceRelease(color);
    if (!context) { CGImageRelease(source); return AGError(3, @"截图缩放缓冲区分配失败"); }
    AGDrawImage(context, source, w, h, rotation); CGImageRelease(source);
    CGImageRef image = CGBitmapContextCreateImage(context); CGContextRelease(context);
    if (!image) return AGError(3, @"截图转换失败");
    NSMutableData *encoded = [NSMutableData data]; BOOL jpeg = [args[@"jpeg"] boolValue];
    CGImageDestinationRef destination = CGImageDestinationCreateWithData((CFMutableDataRef)encoded, jpeg ? CFSTR("public.jpeg") : CFSTR("public.png"), 1, NULL);
    if (!destination) { CGImageRelease(image); return AGError(3, @"图片编码器不可用"); }
    NSDictionary *options = @{(id)kCGImageDestinationLossyCompressionQuality: args[@"quality"] ?: @0.85};
    CGImageDestinationAddImage(destination, image, (CFDictionaryRef)options);
    BOOL ok = CGImageDestinationFinalize(destination); CFRelease(destination); CGImageRelease(image);
    if (!ok) return AGError(3, @"图片编码失败");
    ag_ios_result result = AGData(encoded); result.width = w; result.height = h; return result;
}
