// ag_rec.mm —— 识别栈实现：按参考实现的 C ABI 调用它的 4 个 dylib
//
// 【为什么是 .mm 而不是 .m】
// 它们的入口收 cv::Mat&（C++ 对象），我们必须用 C++ 构造 Mat，
// 所以这个文件必须是 ObjC++。
//
// 【为什么用 dlsym 而不是链接】
// 这些 dylib 是运行时可选的（和 SDK 本身一样可以热更/不装），
// 直接链接会让 App 在没这些库时起不来。dlsym 则能"有就用、没有就明确报错"。
#import "ag_rec.h"
#import "runtime.h"
#include <dlfcn.h>
#include <vector>

#if __has_include(<opencv2/core.hpp>)
#import <opencv2/core.hpp>
#import <opencv2/imgcodecs.hpp>
#import <opencv2/imgproc.hpp>
#define AG_HAS_OPENCV 1
#else
#define AG_HAS_OPENCV 0
#endif

// ---- 参考实现的 C 入口签名（从它的导出符号表逆向得到）----

typedef void *(*AGPpocrNew)(void);
typedef int (*AGPpocrLoad)(void *ctx, const char *det, const char *rec, const char *cls, int threads);
typedef int (*AGPpocrOcr)(void *ctx, void *mat, char *out, int outLen);
typedef void (*AGPpocrClose)(void *ctx);
typedef int (*AGPpocrSetTargetSize)(void *ctx, int sideLen);
typedef int (*AGPpocrSetThreshold)(void *ctx, float boxThresh, float boxScore, float unclip);

typedef void *(*AGYoloNew)(void);
typedef int (*AGYoloLoad)(void *ctx, const char *param, const char *bin, int targetSize, int threads);
typedef int (*AGYoloDetect)(void *ctx, void *mat, int *count, float **boxes, float threshold, float nms);
typedef void (*AGYoloClose)(void *ctx);

typedef void (*AGDotocrSetDict)(const char *dict);
typedef int (*AGDotocrDetect)(void *mat, char *out, int outLen);
typedef int (*AGDotocrFindStr)(void *mat, const char *text, char *out, int outLen);

// ---- 运行时状态 ----

static void *gPpocrHandle;
static void *gYoloHandle;
static void *gDotocrHandle;
static BOOL gTriedLoad;

static AGPpocrNew p_ppocr_new;
static AGPpocrLoad p_ppocr_load;
static AGPpocrOcr p_ppocr_ocr;
static AGPpocrClose p_ppocr_close;
static AGPpocrSetTargetSize p_ppocr_set_target_size;
static AGPpocrSetThreshold p_ppocr_set_threshold;

static AGYoloNew p_yolo_new;
static AGYoloLoad p_yolo_load;
static AGYoloDetect p_yolo_detect;
static AGYoloClose p_yolo_close;

static AGDotocrSetDict p_dotocr_set_dict;
static AGDotocrDetect p_dotocr_detect;
static AGDotocrFindStr p_dotocr_find_str;

static NSString *gUnavailable;

/// 从 App 包里按参考实现同路径加载：<bundle>/Runtime/libXxx.dylib
static void *AGLoadRuntimeLib(NSString *name) {
    NSString *path = [[[NSBundle mainBundle] bundlePath] stringByAppendingPathComponent:
                      [NSString stringWithFormat:@"Runtime/%@", name]];
    if (![[NSFileManager defaultManager] fileExistsAtPath:path]) {
        return NULL;
    }
    return dlopen(path.fileSystemRepresentation, RTLD_NOW | RTLD_GLOBAL);
}

static void AGLoadRecognitionOnce(void) {
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        gTriedLoad = YES;
        if (!AG_HAS_OPENCV) {
            // 头文件都没有，就没法构造 cv::Mat → 直接判不可用，别等运行时崩
            gUnavailable = @"编译期没有 OpenCV 头（opencv2.framework 未就位）";
            return;
        }
        // 【必须按依赖顺序加载】
        // 逆向出的依赖链：
        //   libppocr → libncnn + libopencv_{core,imgproc,imgcodecs}
        //   libyolo  → libncnn
        //   libdotocr → libopencv_{core,imgproc}
        // 而且它们的 LC_RPATH 指向作者本机路径（/Users/song/...），
        // 所以先把被依赖的用 RTLD_GLOBAL 装进来，@rpath 查找就会命中已加载的镜像。
        AGLoadRuntimeLib(@"libopencv_core.dylib");
        AGLoadRuntimeLib(@"libopencv_imgproc.dylib");
        AGLoadRuntimeLib(@"libopencv_imgcodecs.dylib");
        AGLoadRuntimeLib(@"libncnn.dylib");

        gPpocrHandle = AGLoadRuntimeLib(@"libppocr.dylib");
        gYoloHandle = AGLoadRuntimeLib(@"libyolo.dylib");
        gDotocrHandle = AGLoadRuntimeLib(@"libdotocr.dylib");

        if (gPpocrHandle == NULL) {
            gUnavailable = @"缺少 Runtime/libppocr.dylib";
            return;
        }
        p_ppocr_new = (AGPpocrNew)dlsym(gPpocrHandle, "ppocr_new");
        p_ppocr_load = (AGPpocrLoad)dlsym(gPpocrHandle, "ppocr_load");
        p_ppocr_ocr = (AGPpocrOcr)dlsym(gPpocrHandle, "ppocr_ocr");
        p_ppocr_close = (AGPpocrClose)dlsym(gPpocrHandle, "ppocr_close");
        p_ppocr_set_target_size = (AGPpocrSetTargetSize)dlsym(gPpocrHandle, "ppocr_set_target_size");
        p_ppocr_set_threshold = (AGPpocrSetThreshold)dlsym(gPpocrHandle, "ppocr_set_threshold");
        if (!p_ppocr_new || !p_ppocr_load || !p_ppocr_ocr) {
            gUnavailable = @"libppocr.dylib 缺少 ppocr_new/ppocr_load/ppocr_ocr";
            return;
        }
        if (gYoloHandle != NULL) {
            p_yolo_new = (AGYoloNew)dlsym(gYoloHandle, "newYolo");
            p_yolo_load = (AGYoloLoad)dlsym(gYoloHandle, "loadModelYolo");
            p_yolo_detect = (AGYoloDetect)dlsym(gYoloHandle, "detectYolo");
            p_yolo_close = (AGYoloClose)dlsym(gYoloHandle, "closeYolo");
        }
        if (gDotocrHandle != NULL) {
            p_dotocr_set_dict = (AGDotocrSetDict)dlsym(gDotocrHandle, "dotocr_setDict");
            p_dotocr_detect = (AGDotocrDetect)dlsym(gDotocrHandle, "dotocr_detect");
            p_dotocr_find_str = (AGDotocrFindStr)dlsym(gDotocrHandle, "dotocr_findStr");
        }
    });
}

BOOL AGRecognitionAvailable(void) {
    AGLoadRecognitionOnce();
    return gUnavailable == nil && p_ppocr_new != NULL;
}

NSString *AGRecognitionUnavailableReason(void) {
    AGLoadRecognitionOnce();
    if (gUnavailable != nil) {
        return gUnavailable;
    }
    if (p_ppocr_new == NULL) {
        return @"识别栈未初始化";
    }
    return @"";
}

#if AG_HAS_OPENCV
/// 把 base64 PNG/JPEG 解成 cv::Mat（BGR 三通道）。
/// 用 cv::imdecode 而不是自己解码：与参考实现同一条路径，颜色顺序也一致。
static BOOL AGMatFromBase64(NSString *b64, cv::Mat &out) {
    if (b64.length == 0) {
        return NO;
    }
    NSData *data = [[NSData alloc] initWithBase64EncodedString:b64
                                                       options:NSDataBase64DecodingIgnoreUnknownCharacters];
    if (data.length == 0) {
        return NO;
    }
    std::vector<uchar> buf((uchar *)data.bytes, (uchar *)data.bytes + data.length);
    out = cv::imdecode(buf, cv::IMREAD_COLOR);
    return !out.empty();
}
#endif

static ag_ios_result AGRecError(NSString *message) {
    return AGError(1, message);
}

ag_ios_result AGRecognition(NSString *op, NSDictionary *args) {
    AGLoadRecognitionOnce();
    if (!AGRecognitionAvailable()) {
        return AGRecError([@"识别栈不可用：" stringByAppendingString:AGRecognitionUnavailableReason()]);
    }
#if !AG_HAS_OPENCV
    return AGRecError(@"识别栈不可用：编译期缺少 OpenCV");
#else
    // ---------- OCR ----------
    if ([op isEqualToString:@"ppocr.ocr"]) {
        cv::Mat mat;
        if (!AGMatFromBase64(args[@"base64"], mat)) {
            return AGRecError(@"ppocr.ocr：图片解码失败（base64 为空或不是有效图片）");
        }
        void *ctx = p_ppocr_new();
        if (ctx == NULL) {
            return AGRecError(@"ppocr_new 返回空");
        }
        NSString *det = args[@"det"] ?: @"";
        NSString *rec = args[@"rec"] ?: @"";
        NSString *cls = args[@"cls"] ?: @"";
        int threads = [args[@"threads"] intValue];
        (void)p_ppocr_load(ctx, det.UTF8String, rec.UTF8String,
                           cls.length ? cls.UTF8String : NULL, threads);
        if ([args[@"targetSize"] intValue] > 0) {
            (void)p_ppocr_set_target_size(ctx, [args[@"targetSize"] intValue]);
        }
        char buf[256 * 1024];
        int n = p_ppocr_ocr(ctx, &mat, buf, (int)sizeof(buf));
        p_ppocr_close(ctx);
        if (n <= 0 || buf[0] == '\0') {
            return AGJSON(@{@"items": @[]});
        }
        NSString *json = [NSString stringWithUTF8String:buf];
        NSData *d = [json dataUsingEncoding:NSUTF8StringEncoding];
        id obj = d ? [NSJSONSerialization JSONObjectWithData:d options:0 error:NULL] : nil;
        // 原生可能直接返回数组，也可能是 {"items":[...]}：两种都接住
        if ([obj isKindOfClass:[NSArray class]]) {
            return AGJSON(@{@"items": obj});
        }
        if ([obj isKindOfClass:[NSDictionary class]]) {
            return AGJSON(obj);
        }
        return AGRecError(@"ppocr_ocr 返回的不是 JSON");
    }
    if ([op isEqualToString:@"ppocr.close"]) {
        return AGJSON(@{@"closed": @YES});
    }

    // ---------- YOLO ----------
    if ([op isEqualToString:@"yolo.detect_image"] || [op isEqualToString:@"yolo.detect"]) {
        if (p_yolo_detect == NULL) {
            return AGRecError(@"yolo 不可用：缺 Runtime/libyolo.dylib 或入口符号");
        }
        cv::Mat mat;
        if (!AGMatFromBase64(args[@"base64"], mat)) {
            return AGRecError(@"yolo：图片解码失败");
        }
        void *ctx = p_yolo_new ? p_yolo_new() : NULL;
        if (ctx == NULL) {
            return AGRecError(@"newYolo 返回空");
        }
        NSString *param = args[@"param"] ?: @"";
        NSString *bin = args[@"bin"] ?: @"";
        int target = [args[@"targetSize"] intValue];
        int threads = [args[@"threads"] intValue];
        (void)p_yolo_load(ctx, param.UTF8String, bin.UTF8String, target, threads);

        int count = 0;
        float *boxes = NULL;
        float threshold = [args[@"threshold"] floatValue];
        float nms = [args[@"nms"] floatValue];
        int rc = p_yolo_detect(ctx, &mat, &count, &boxes, threshold, nms);
        (void)rc;
        NSMutableArray *items = [NSMutableArray array];
        // 每个框按 [x, y, w, h, score, classId] 六元组解析
        for (int i = 0; i < count && boxes != NULL; i++) {
            float *b = boxes + i * 6;
            [items addObject:@{
                @"x": @((int)b[0]), @"y": @((int)b[1]),
                @"宽": @((int)b[2]), @"高": @((int)b[3]),
                @"精度": @(b[4]), @"标签": @((int)b[5]),
            }];
        }
        if (p_yolo_close) {
            p_yolo_close(ctx);
        }
        return AGJSON(@{@"items": items});
    }
    if ([op isEqualToString:@"yolo.load"] || [op isEqualToString:@"yolo.set_image"]) {
        // 加载/预热在 detect 里一并完成；这里返回成功，保持与 Go 层调用序列一致
        return AGJSON(@{@"ok": @YES});
    }
    if ([op isEqualToString:@"yolo.close"]) {
        return AGJSON(@{@"closed": @YES});
    }

    // ---------- 点阵 OCR ----------
    if ([op isEqualToString:@"dotocr.set_dict"]) {
        if (p_dotocr_set_dict == NULL) {
            return AGRecError(@"dotocr 不可用：缺 Runtime/libdotocr.dylib 或 dotocr_setDict");
        }
        NSString *dict = args[@"dict"] ?: @"";
        p_dotocr_set_dict(dict.UTF8String);
        return AGJSON(@{@"ok": @YES});
    }
    if ([op isEqualToString:@"dotocr.ocr"] || [op isEqualToString:@"dotocr.find"]) {
        if (p_dotocr_detect == NULL && p_dotocr_find_str == NULL) {
            return AGRecError(@"dotocr 不可用：缺入口符号");
        }
        cv::Mat mat;
        if (!AGMatFromBase64(args[@"base64"], mat)) {
            return AGRecError(@"dotocr：图片解码失败");
        }
        char buf[64 * 1024];
        int n = 0;
        if ([op isEqualToString:@"dotocr.find"] && p_dotocr_find_str) {
            NSString *text = args[@"text"] ?: @"";
            n = p_dotocr_find_str(&mat, text.UTF8String, buf, (int)sizeof(buf));
        } else if (p_dotocr_detect) {
            n = p_dotocr_detect(&mat, buf, (int)sizeof(buf));
        }
        if (n <= 0 || buf[0] == '\0') {
            return AGJSON(@{@"items": @[]});
        }
        NSString *json = [NSString stringWithUTF8String:buf];
        NSData *d = [json dataUsingEncoding:NSUTF8StringEncoding];
        id obj = d ? [NSJSONSerialization JSONObjectWithData:d options:0 error:NULL] : nil;
        if ([obj isKindOfClass:[NSArray class]]) {
            return AGJSON(@{@"items": obj});
        }
        if ([obj isKindOfClass:[NSDictionary class]]) {
            return AGJSON(obj);
        }
        return AGJSON(@{@"text": json});
    }

    return AGError(2, [NSString stringWithFormat:@"未知识别方法: %@", op]);
#endif
}
