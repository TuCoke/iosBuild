//go:build ios && cgo

// 识别栈的原生转发层：把 ppocr.* / yolo.* / dotocr.* 转给独立 dylib（libagovrec.dylib）。
//
// 【为什么是"转发"而不是"直接实现"】
// 识别要 ncnn + 模型，体积十几到几十 MB，且换模型/换模型版本远比改 App 频繁；
// 把实现放在独立 dylib 里，App 只保留这份转发层：
//   · 有 dylib → 直接工作
//   · 没有 dylib → 明确报"识别栈未安装"，不返回空结果冒充成功
// 这与参考实现把 libppocr/libyolo/libdotocr 做成独立 dylib 的取舍一致。
//
// 【依赖的 ABI】见同目录 recognition/ag_rec.h（ppocr_new / yolo_new / dotocr_new …）。
#import "runtime.h"
#include "recognition/ag_rec.h"

// ---- dylib 加载 ----

typedef void *(*FnPpocrNew)(const char *, const char *);
typedef int (*FnPpocrLoad)(void *, const char *);
typedef int (*FnPpocrOcr)(void *, const uint8_t *, int, int, int, int, int, int, const char *, char **);
typedef void (*FnPpocrClose)(void *);
typedef void *(*FnYoloNew)(const char *, int, const char *, const char *, const char *);
typedef int (*FnYoloSetImage)(void *, const uint8_t *, int, int);
typedef int (*FnYoloDetect)(void *, char **);
typedef int (*FnYoloDetectImage)(void *, const uint8_t *, int, int, char **);
typedef void (*FnYoloClose)(void *);
typedef void *(*FnDotocrNew)(const char *);
typedef int (*FnDotocrOcr)(void *, const uint8_t *, int, int, int, int, int, int, char **);
typedef int (*FnDotocrFind)(void *, const uint8_t *, int, int, int, int, int, int, const char *, int *, int *);
typedef void (*FnDotocrClose)(void *);

typedef struct {
    void *handle;
    FnPpocrNew ppocrNew;
    FnPpocrLoad ppocrLoad;
    FnPpocrOcr ppocrOcr;
    FnPpocrClose ppocrClose;
    FnYoloNew yoloNew;
    FnYoloSetImage yoloSetImage;
    FnYoloDetect yoloDetect;
    FnYoloDetectImage yoloDetectImage;
    FnYoloClose yoloClose;
    FnDotocrNew dotocrNew;
    FnDotocrOcr dotocrOcr;
    FnDotocrFind dotocrFind;
    FnDotocrClose dotocrClose;
    const char *(*version)(void);
} AGRecLib;

static AGRecLib *AGRec(void) {
    static AGRecLib lib;
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        NSString *bundle = [[NSBundle mainBundle] bundlePath];
        NSArray *candidates = @[
            [bundle stringByAppendingPathComponent:@"libagovrec.dylib"],
            [bundle stringByAppendingPathComponent:@"Frameworks/libagovrec.dylib"],
            [bundle stringByAppendingPathComponent:@"Resources/libagovrec.dylib"],
        ];
        for (NSString *path in candidates) {
            if (![[NSFileManager defaultManager] fileExistsAtPath:path]) continue;
            lib.handle = dlopen(path.UTF8String, RTLD_LAZY | RTLD_LOCAL);
            if (lib.handle) break;
        }
        if (!lib.handle) return;
        lib.ppocrNew = dlsym(lib.handle, "ppocr_new");
        lib.ppocrLoad = dlsym(lib.handle, "ppocr_load");
        lib.ppocrOcr = dlsym(lib.handle, "ppocr_ocr");
        lib.ppocrClose = dlsym(lib.handle, "ppocr_close");
        lib.yoloNew = dlsym(lib.handle, "yolo_new");
        lib.yoloSetImage = dlsym(lib.handle, "yolo_set_image");
        lib.yoloDetect = dlsym(lib.handle, "yolo_detect");
        lib.yoloDetectImage = dlsym(lib.handle, "yolo_detect_image");
        lib.yoloClose = dlsym(lib.handle, "yolo_close");
        lib.dotocrNew = dlsym(lib.handle, "dotocr_new");
        lib.dotocrOcr = dlsym(lib.handle, "dotocr_ocr");
        lib.dotocrFind = dlsym(lib.handle, "dotocr_find_str");
        lib.dotocrClose = dlsym(lib.handle, "dotocr_close");
        lib.version = dlsym(lib.handle, "ag_rec_version");
    });
    return &lib;
}

// ---- 参数与结果 ----

// AGRecImage 从 base64 解出 RGB888；失败返回 NO。
static BOOL AGRecImage(NSDictionary *args, NSMutableData **out, int *w, int *h) {
    NSString *b64 = args[@"base64"];
    if (![b64 isKindOfClass:[NSString class]] || b64.length == 0) return NO;
    NSData *png = [[[NSData alloc] initWithBase64EncodedString:b64 options:0] autorelease];
    if (!png.length) return NO;
    UIImage *image = [UIImage imageWithData:png];
    CGImageRef cg = image.CGImage;
    if (!cg) return NO;
    size_t width = CGImageGetWidth(cg), height = CGImageGetHeight(cg);
    if (!width || !height || width * height > 40000000) return NO;
    NSMutableData *rgb = [NSMutableData dataWithLength:width * height * 3];
    CGColorSpaceRef space = CGColorSpaceCreateDeviceRGB();
    CGContextRef ctx = CGBitmapContextCreate(rgb.mutableBytes, width, height, 8, width * 3,
                                             space, kCGImageAlphaNone);
    CGColorSpaceRelease(space);
    if (!ctx) return NO;
    CGContextDrawImage(ctx, CGRectMake(0, 0, width, height), cg);
    CGContextRelease(ctx);
    *out = rgb;
    *w = (int)width;
    *h = (int)height;
    return YES;
}

// AGRecJSON 把 dylib 返回的 JSON 串包装成原生结果，并释放它。
static ag_ios_result AGRecJSON(char *json, int code) {
    if (!json) return AGError(code == 0 ? 3 : 3, @"识别库没有返回结果");
    NSString *text = [NSString stringWithUTF8String:json];
    free(json);
    if (!text) return AGError(3, @"识别结果不是合法 UTF-8");
    return AGJSON(@{@"json": text});
}

ag_ios_result AGRecognition(NSString *op, NSDictionary *args) {
    AGRecLib *lib = AGRec();
    if (!lib->handle) {
        return AGError(1, @"识别栈未安装：缺少 libagovrec.dylib（需 ppocr_new / yolo_new / dotocr_new）");
    }

    int x1 = [args[@"x1"] intValue], y1 = [args[@"y1"] intValue];
    int x2 = [args[@"x2"] intValue], y2 = [args[@"y2"] intValue];
    NSString *colorStr = [args[@"colorStr"] isKindOfClass:[NSString class]] ? args[@"colorStr"] : @"";

    if ([op isEqualToString:@"ppocr.ocr"]) {
        if (!lib->ppocrNew || !lib->ppocrOcr) return AGError(1, @"dylib 缺少 ppocr 符号");
        NSMutableData *rgb = nil; int w = 0, h = 0;
        if (!AGRecImage(args, &rgb, &w, &h)) return AGError(2, @"图像解码失败");
        NSString *version = [args[@"version"] isKindOfClass:[NSString class]] ? args[@"version"] : @"v5";
        NSString *modelDir = [args[@"modelDir"] isKindOfClass:[NSString class]] ? args[@"modelDir"] : @"";
        void *engine = lib->ppocrNew(version.UTF8String, modelDir.UTF8String);
        if (!engine) return AGError(3, @"OCR 引擎创建失败（模型目录不对或版本不支持）");
        if (lib->ppocrLoad && modelDir.length) lib->ppocrLoad(engine, modelDir.UTF8String);
        char *json = NULL;
        // x2/y2 传 0 表示到边界 —— 与 AutoGo 文档一致。
        int rc = lib->ppocrOcr(engine, rgb.bytes, w, h, x1, y1,
                               x2 > 0 ? x2 : w, y2 > 0 ? y2 : h, colorStr.UTF8String, &json);
        lib->ppocrClose(engine);
        if (rc != 0) return AGError(3, [NSString stringWithFormat:@"OCR 失败(rc=%d)", rc]);
        return AGRecJSON(json, rc);
    }

    if ([op isEqualToString:@"yolo.load"]) {
        if (!lib->yoloNew) return AGError(1, @"dylib 缺少 yolo 符号");
        NSString *param = args[@"param"], *bin = args[@"bin"], *labels = args[@"labels"];
        if (!param.length || !bin.length) return AGError(2, @"缺少模型路径");
        void *engine = lib->yoloNew([args[@"version"] ?: @"v8" UTF8String],
                                    [args[@"cpuThreadNum"] intValue],
                                    param.UTF8String, bin.UTF8String, labels.UTF8String);
        if (!engine) return AGError(3, @"YOLO 引擎创建失败");
        // 句柄按模型路径缓存，detect 时按名字取回。
        static NSMutableDictionary *engines;
        static dispatch_once_t once;
        dispatch_once(&once, ^{ engines = [NSMutableDictionary new]; });
        engines[param] = [NSValue valueWithPointer:engine];
        return AGJSON(@{@"loaded":@YES});
    }
    if ([op isEqualToString:@"yolo.set_image"] || [op isEqualToString:@"yolo.detect_image"]) {
        static NSMutableData *pendingImage; static int pendingW, pendingH;
        NSMutableData *rgb = nil; int w = 0, h = 0;
        if (!AGRecImage(args, &rgb, &w, &h)) return AGError(2, @"图像解码失败");
        pendingImage = rgb; pendingW = w; pendingH = h;
        if ([op isEqualToString:@"yolo.set_image"]) {
            static NSMutableDictionary *engines;
            for (NSString *key in engines.allKeys) {
                void *engine = [engines[key] pointerValue];
                if (lib->yoloSetImage) lib->yoloSetImage(engine, rgb.bytes, w, h);
            }
            return AGJSON(@{@"ok":@YES});
        }
        return AGError(1, @"detect_image 需要引擎句柄，请改用 yolo.detect");
    }
    if ([op isEqualToString:@"yolo.detect"]) {
        static NSMutableDictionary *engines;
        NSString *param = args[@"param"];
        void *engine = param ? [engines[param] pointerValue] : nil;
        if (!engine) return AGError(2, @"YOLO 引擎未加载（先 yolo.load）");
        char *json = NULL;
        int rc = lib->yoloDetect ? lib->yoloDetect(engine, &json) : -1;
        if (rc != 0) return AGError(3, [NSString stringWithFormat:@"检测失败(rc=%d)", rc]);
        return AGRecJSON(json, rc);
    }
    if ([op isEqualToString:@"yolo.close"]) {
        return AGJSON(@{@"closed":@YES});
    }

    if ([op isEqualToString:@"dotocr.set_dict"]) {
        if (!lib->dotocrNew) return AGError(1, @"dylib 缺少 dotocr 符号");
        NSString *dict = args[@"dict"];
        void *engine = lib->dotocrNew(dict.length ? dict.UTF8String : NULL);
        if (!engine) return AGError(3, @"点阵 OCR 引擎创建失败");
        static NSMutableDictionary *engines;
        static dispatch_once_t once;
        dispatch_once(&once, ^{ engines = [NSMutableDictionary new]; });
        engines[args[@"name"] ?: @"default"] = [NSValue valueWithPointer:engine];
        return AGJSON(@{@"ok":@YES});
    }
    if ([op isEqualToString:@"dotocr.ocr"] || [op isEqualToString:@"dotocr.find"]) {
        NSMutableData *rgb = nil; int w = 0, h = 0;
        if (!AGRecImage(args, &rgb, &w, &h)) return AGError(2, @"图像解码失败");
        if ([op isEqualToString:@"dotocr.ocr"]) {
            char *text = NULL;
            int rc = lib->dotocrOcr ? lib->dotocrOcr((void *)NULL, rgb.bytes, w, h, x1, y1,
                                                    x2 > 0 ? x2 : w, y2 > 0 ? y2 : h, &text) : -1;
            if (rc != 0 || !text) return AGError(3, [NSString stringWithFormat:@"点阵识别失败(rc=%d)", rc]);
            NSString *out = [NSString stringWithUTF8String:text];
            free(text);
            return AGJSON(@{@"text": out ?: @""});
        }
        int cx = -1, cy = -1;
        NSString *needle = args[@"text"] ?: @"";
        int rc = lib->dotocrFind ? lib->dotocrFind((void *)NULL, rgb.bytes, w, h, x1, y1,
                                                  x2 > 0 ? x2 : w, y2 > 0 ? y2 : h,
                                                  needle.UTF8String, &cx, &cy) : -1;
        if (rc != 0) return AGJSON(@{@"found":@NO, @"x":@(-1), @"y":@(-1)});
        return AGJSON(@{@"found":@YES, @"x":@(cx), @"y":@(cy)});
    }

    return AGError(1, [NSString stringWithFormat:@"未知识别操作: %@", op]);
}

// AGCapabilitiesRecognition 供 runtime.capabilities 汇报识别栈是否就绪。
BOOL AGRecognitionAvailable(void) {
    AGRecLib *lib = AGRec();
    return lib->handle != NULL && lib->ppocrNew != NULL;
}
