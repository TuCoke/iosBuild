// 根据参考样本的 cgo 实际调用布局声明 ABI，不能从导出函数名猜参数。
// 输入为连续 RGBA 像素，返回的 C 字符串由调用方 free；识别引擎内部仍使用 NCNN/OpenCV。
#import "ag_rec.h"
#import "runtime.h"
#include <dlfcn.h>
#include <stdlib.h>
#include <string.h>
#include <exception>

typedef void *(*AGPpocrNew)(int);
typedef int (*AGPpocrLoad)(void *,int);
typedef char *(*AGPpocrOcr)(void *,const uint8_t *,int,int,const char *);
typedef void (*AGPpocrClose)(void *);
typedef void *(*AGYoloNew)(void);
typedef char *(*AGYoloLoad)(void *,const char *,const char *,const char *,const char *,int);
typedef char *(*AGYoloDetect)(void *,const uint8_t *,int,int,float,float,int);
typedef void (*AGYoloClose)(void *);
typedef void (*AGDotSet)(const char *,const char *);
typedef char *(*AGDotDetect)(const uint8_t *,int,int,const char *,float,int,const char *,int,int);
typedef void (*AGDotFind)(const uint8_t *,int,int,const char *,float,const char *,const char *,int,int,int *,int *);
static AGPpocrNew ppNew;static AGPpocrLoad ppLoad;static AGPpocrOcr ppOcr;static AGPpocrClose ppClose;
static AGYoloNew yNew;static AGYoloLoad yLoad;static AGYoloDetect yDetect;static AGYoloClose yClose;
static AGDotSet dotSet;static AGDotDetect dotDetect;static AGDotFind dotFind;
static NSRecursiveLock *engineLock;static NSMutableDictionary *engines;
static void *AGRecLibrary(NSString *name){
 NSString *path=[AGApplicationBundlePath() stringByAppendingPathComponent:[@"Runtime/" stringByAppendingString:name]];
 return dlopen(path.fileSystemRepresentation,RTLD_NOW|RTLD_GLOBAL);
}
static void AGRecInit(void){static dispatch_once_t once;dispatch_once(&once,^{
 engineLock=[NSRecursiveLock new];engines=[NSMutableDictionary new];
 // 依赖先加载；按能力检查各自符号，不因缺 OCR 而一并禁用 YOLO 或点阵识别。
 for(NSString *name in @[@"libncnn.dylib",@"libopencv_core.dylib",@"libopencv_imgproc.dylib",@"libopencv_imgcodecs.dylib"])AGRecLibrary(name);
 void *pp=AGRecLibrary(@"libppocr.dylib"),*y=AGRecLibrary(@"libyolo.dylib"),*dot=AGRecLibrary(@"libdotocr.dylib");
 if(pp){ppNew=(AGPpocrNew)dlsym(pp,"ppocr_new");ppLoad=(AGPpocrLoad)dlsym(pp,"ppocr_load");ppOcr=(AGPpocrOcr)dlsym(pp,"ppocr_ocr");ppClose=(AGPpocrClose)dlsym(pp,"ppocr_close");}
 if(y){yNew=(AGYoloNew)dlsym(y,"newYolo");yLoad=(AGYoloLoad)dlsym(y,"loadModelYolo");yDetect=(AGYoloDetect)dlsym(y,"detectYolo");yClose=(AGYoloClose)dlsym(y,"closeYolo");}
 if(dot){dotSet=(AGDotSet)dlsym(dot,"dotocr_setDict");dotDetect=(AGDotDetect)dlsym(dot,"dotocr_detect");dotFind=(AGDotFind)dlsym(dot,"dotocr_findStr");}
});}
@interface AGRecEngine:NSObject {
@public void *handle;BOOL yolo;NSData *image;int width,height;
}
@end
@implementation AGRecEngine
- (void)dealloc {if(handle){if(yolo && yClose)yClose(handle);else if(!yolo && ppClose)ppClose(handle);}}
@end
BOOL AGRecognitionAvailable(void){AGRecInit();return (ppNew && ppLoad && ppOcr && ppClose) || (yNew && yLoad && yDetect && yClose) || (dotSet && dotDetect && dotFind);}
NSString *AGRecognitionUnavailableReason(void){return AGRecognitionAvailable()?@"":@"Runtime 识别库缺失或 ABI 入口不完整";}

// UIImage 解码后展开为连续 RGBA，匹配样本传入 image.NRGBA.Pix 的布局。
static NSData *AGRGBA(NSString *base64,int *w,int *h){
 if(![base64 isKindOfClass:[NSString class]] || base64.length>48*1024*1024)return nil;
 NSData *encoded=[[NSData alloc] initWithBase64EncodedString:base64 options:0];
 UIImage *image=[UIImage imageWithData:encoded];CGImageRef cg=image.CGImage;if(!cg)return nil;CGImageRetain(cg);
 size_t width=CGImageGetWidth(cg),height=CGImageGetHeight(cg);if(!width || !height || width>8192 || height>8192 || width*height>16*1024*1024){CGImageRelease(cg);return nil;}
 NSMutableData *rgba=[NSMutableData dataWithLength:width*height*4];CGColorSpaceRef color=CGColorSpaceCreateDeviceRGB();
 CGContextRef context=CGBitmapContextCreate(rgba.mutableBytes,width,height,8,width*4,color,kCGImageAlphaPremultipliedLast|kCGBitmapByteOrder32Big);CGColorSpaceRelease(color);if(!context){CGImageRelease(cg);return nil;}
 CGContextDrawImage(context,CGRectMake(0,0,width,height),cg);CGContextRelease(context);CGImageRelease(cg);
 // 解预乘，保持与 Go NRGBA 的非预乘色值一致。
 uint8_t *pixels=(uint8_t *)rgba.mutableBytes;for(size_t i=0;i<rgba.length;i+=4){unsigned a=pixels[i+3];if(a && a<255)for(int c=0;c<3;c++)pixels[i+c]=(uint8_t)MIN(255,(pixels[i+c]*255+a/2)/a);}
 *w=(int)width;*h=(int)height;return rgba;
}
static NSString *AGOwnedString(char *value){if(!value)return nil;size_t size=strnlen(value,4*1024*1024+1);NSString *out=size<=4*1024*1024?[[NSString alloc] initWithBytes:value length:size encoding:NSUTF8StringEncoding]:nil;free(value);return out;}
static ag_ios_result AGItems(char *value){NSString *text=AGOwnedString(value);if(!text)return AGError(3,@"识别返回空指针、超长数据或无效 UTF-8");NSData *data=[text dataUsingEncoding:NSUTF8StringEncoding];id parsed=[NSJSONSerialization JSONObjectWithData:data options:0 error:NULL];if([parsed isKindOfClass:[NSArray class]])return AGJSON(@{@"items":parsed});if([parsed isKindOfClass:[NSDictionary class]] && [parsed[@"items"] isKindOfClass:[NSArray class]])return AGJSON(parsed);return AGError(3,[@"识别响应不是结果数组: " stringByAppendingString:text]);}

struct AGRecGuard{__unsafe_unretained NSRecursiveLock *lock;AGRecGuard(NSRecursiveLock *value):lock(value){[lock lock];}~AGRecGuard(){[lock unlock];}};
ag_ios_result AGRecognition(NSString *op,NSDictionary *args){
 AGRecInit();AGRecGuard guard(engineLock);try{@try{
 BOOL isYolo=[op hasPrefix:@"yolo."],isPP=[op hasPrefix:@"ppocr."];
 if(isYolo || isPP){
  NSNumber *token=args[@"token"];if(![token isKindOfClass:[NSNumber class]] || token.unsignedLongLongValue==0)return AGError(2,@"缺少模型实例标识");
  NSString *key=[NSString stringWithFormat:@"%@:%@",isYolo?@"yolo":@"ppocr",token];AGRecEngine *engine=engines[key];
  if([op hasSuffix:@".close"]){[engines removeObjectForKey:key];return AGJSON(@{@"closed":@YES});}
  if([op hasSuffix:@".load"]){
   if(engine)return AGJSON(@{@"ok":@YES});if(engines.count>=16)return AGError(5,@"模型实例过多，请先关闭不用的引擎");
   if(isYolo && !(yNew && yLoad && yDetect && yClose))return AGError(1,@"YOLO 动态库或入口不完整");
   if(isPP && !(ppNew && ppLoad && ppOcr && ppClose))return AGError(1,@"PPOCR 动态库或入口不完整");
   AGRecEngine *created=[[AGRecEngine alloc] init];created->yolo=isYolo;
   if(isYolo){
    NSString *param=args[@"param"],*bin=args[@"bin"],*labels=args[@"labels"] ?: @"",*version=[args[@"version"] lowercaseString];
    if(!param.length || !bin.length || ![[NSFileManager defaultManager] fileExistsAtPath:param] || ![[NSFileManager defaultManager] fileExistsAtPath:bin]){return AGError(2,@"模型 param/bin 文件不存在");}
    int threads=[args[@"threads"] intValue];if(threads<0)threads=0;if(threads>64){return AGError(2,@"CPU 线程数越界");}
    created->handle=yNew();if(!created->handle){return AGError(3,@"YOLO 实例创建失败");}
    NSString *status=AGOwnedString(yLoad(created->handle,(version ?: @"").UTF8String,param.UTF8String,bin.UTF8String,labels.UTF8String,threads));
    if(![status isEqualToString:@"OK"]){return AGError(3,status ?: @"YOLO 模型加载返回无效响应");}
   }else{
    int version=[[args[@"version"] lowercaseString] isEqualToString:@"v2"]?2:5;created->handle=ppNew(version);
    if(!created->handle || ppLoad(created->handle,1)!=0){return AGError(3,@"PPOCR 模型加载失败");}
   }
   engines[key]=created;return AGJSON(@{@"ok":@YES});
  }
  if(!engine)return AGError(7,@"模型未加载或实例已关闭");
  if([op isEqualToString:@"yolo.set_image"]){int w=0,h=0;NSData *pixels=AGRGBA(args[@"base64"],&w,&h);if(!pixels)return AGError(2,@"预置图片解码失败");engine->image=[pixels copy];engine->width=w;engine->height=h;return AGJSON(@{@"ok":@YES});}
  int w=0,h=0;NSData *pixels=AGRGBA(args[@"base64"],&w,&h);
  if(!pixels && [op isEqualToString:@"yolo.detect"]){pixels=engine->image;w=engine->width;h=engine->height;}
  if(!pixels)return AGError(2,@"识别图片解码失败或没有预置图像");
  if(isPP && [op isEqualToString:@"ppocr.ocr"])return AGItems(ppOcr(engine->handle,(const uint8_t *)pixels.bytes,w,h,(args[@"color"] ?: @"").UTF8String));
  if(isYolo && ([op isEqualToString:@"yolo.detect"] || [op isEqualToString:@"yolo.detect_image"])){
   float probability=args[@"threshold"]?[args[@"threshold"] floatValue]:0.35f,nms=args[@"nms"]?[args[@"nms"] floatValue]:0.25f;int target=[args[@"targetSize"] intValue];if(target<=0)target=640;
   if(probability<0 || probability>1 || nms<0 || nms>1 || target>4096)return AGError(2,@"YOLO 检测阈值或尺寸越界");
   return AGItems(yDetect(engine->handle,(const uint8_t *)pixels.bytes,w,h,probability,nms,target));
  }
  return AGError(1,@"未知模型操作");
 }
 if([op isEqualToString:@"dotocr.set_dict"]){if(!dotSet)return AGError(1,@"点阵字典入口不可用");NSString *name=args[@"name"],*dict=args[@"dict"];if(!name.length || !dict.length)return AGError(2,@"字典名与内容不能为空");dotSet(name.UTF8String,dict.UTF8String);return AGJSON(@{@"ok":@YES});}
 if([op isEqualToString:@"dotocr.ocr"] || [op isEqualToString:@"dotocr.find"]){
  int w=0,h=0;NSData *pixels=AGRGBA(args[@"base64"],&w,&h);if(!pixels)return AGError(2,@"点阵识别图片解码失败");
  NSString *threshold=args[@"threshold"] ?: @"",*dict=args[@"dict"] ?: @"";float sim=[args[@"sim"] floatValue];if(sim<0 || sim>1)return AGError(2,@"相似度越界");NSArray *region=args[@"region"];int ox=0,oy=0;if([region isKindOfClass:[NSArray class]] && region.count>=2){ox=[region[0] intValue];oy=[region[1] intValue];}
  if([op isEqualToString:@"dotocr.ocr"]){if(!dotDetect)return AGError(1,@"点阵识别入口不可用");NSString *text=AGOwnedString(dotDetect((const uint8_t *)pixels.bytes,w,h,threshold.UTF8String,sim,[args[@"json"] boolValue],dict.UTF8String,ox,oy));return text?AGJSON(@{@"text":text}):AGError(3,@"点阵识别返回无效数据");}
  if(!dotFind)return AGError(1,@"点阵查找入口不可用");NSString *text=args[@"text"];if(!text.length)return AGError(2,@"查找文本不能为空");int x=-1,y=-1;dotFind((const uint8_t *)pixels.bytes,w,h,threshold.UTF8String,sim,text.UTF8String,dict.UTF8String,ox,oy,&x,&y);return AGJSON(@{@"x":@(x),@"y":@(y)});
 }
 return AGError(1,@"未知识别操作");
 }@catch(NSException *exception){return AGError(3,[@"识别异常: " stringByAppendingString:exception.reason ?: @""]);}
 }catch(const std::exception &failure){return AGError(3,[NSString stringWithUTF8String:failure.what()] ?: @"识别引擎异常");}
 catch(...){return AGError(3,@"识别引擎出现未知 C++ 异常");}
}
