#import <Foundation/Foundation.h>
#import <UIKit/UIKit.h>
#import <CoreGraphics/CoreGraphics.h>
#import <dlfcn.h>
#import <objc/message.h>
#include <math.h>
#include "bridge.h"

// 【必须包 extern "C"】
// ag_rec.mm / imgui_overlay.mm 是 ObjC++，按 C++ 规则编。
// 如果这些声明没有 C 链接，ObjC++ 侧会去找 C++ 修饰过的符号（如 AGJSON(objc_object*)），
// 而实现（runtime_ios.m 等）导出的是 C 符号 _AGJSON —— 链接期就互相找不到。
#ifdef __cplusplus
extern "C" {
#endif

ag_ios_result AGError(int code, NSString *message);
ag_ios_result AGData(NSData *data);
ag_ios_result AGJSON(id object);
ag_ios_result AGOnMain(ag_ios_result (^work)(void));
void *AGSymbol(const char *name);
void AGLoadFrameworks(void);
NSString *AGApplicationBundlePath(void);
ag_ios_result AGDisplayInfo(void);
int AGScreenRotation(void);
ag_ios_result AGDevice(NSString *op, NSDictionary *args);
ag_ios_result AGInstallPackage(NSString *path, BOOL *handled);
ag_ios_result AGUninstallPackage(NSString *identifier, BOOL *handled);
ag_ios_result AGApplication(NSString *op, NSDictionary *args);
ag_ios_result AGInput(NSString *op, NSDictionary *args);
BOOL AGInputAvailable(NSString *kind);
ag_ios_result AGCapture(NSDictionary *args);
ag_ios_result AGDiagnostics(void);
NSDictionary *AGCaptureProbe(NSArray *displayNames,int width,int height);
// 返回独立拥有的图像，调用者必须 CGImageRelease；采集资源在内部串行保护。
CGImageRef AGCopyScreenImage(int width, int height, NSString **error);
void AGReleaseCaptureSurface(void);
void AGDrawImage(CGContextRef context, CGImageRef image, int width, int height, int rotation);
// 编码与原始帧回调只在回调期间借用数据，跨回调保留时必须复制。
typedef void (*AGH264Callback)(const uint8_t *, int);
typedef void (*AGRawCallback)(const uint8_t *, int, int, int);
void AGStreamFailure(NSString *message);
char *AGStreamFailureCopy(void);
BOOL AGPublishH264(NSData *data, BOOL keyframe);
int AGH264CallbackSet(AGH264Callback cb);
int AGH264ParamsSet(int resolution,int bitrate,int fps);
int AGH264Start(void);
int AGH264Stop(void);
int AGRawCallbackSet(AGRawCallback cb);
int AGRawStart(void);
int AGRawStop(void);
ag_ios_result AGVideo(NSString *op, NSDictionary *args);
ag_ios_result AGSystem(NSString *op, NSDictionary *args);
ag_ios_result AGUtils(NSString *op, NSDictionary *args);
int AXBootstrap(void);
ag_ios_result AGAXForegroundApplication(void);
ag_ios_result AGAXEnvironment(BOOL restore);
ag_ios_result AGAXWarmup(NSDictionary *args);
ag_ios_result AGAXRestoreAfterWarmup(void);
char *AXSweepNodesJSON(char *buf, size_t bufLen);
NSArray *AGAXCollectNodes(NSUInteger limit, NSString **error);
ag_ios_result AGUIAcc(NSString *op, NSDictionary *args);
ag_ios_result AGOverlay(NSString *op, NSDictionary *args);
ag_ios_result AGAPKCtl(NSString *op, NSDictionary *args);
ag_ios_result AGSelfTest(NSString *op, NSDictionary *args);
// 悬浮层（imgui + Metal）：hud.* / console.* 的实现
ag_ios_result AGImGuiOverlay(NSString *op, NSDictionary *args);
BOOL AGImGuiOverlayAvailable(void);

// 进程间共享输入租约；只约束 SDK 协作进程，不替代云端租户/Session 鉴权。
NSString *AGRuntimeDocumentsPath(void);
ag_ios_result AGInputLeaseAcquire(void);
ag_ios_result AGRecoverOrphanTouches(void);
void AGInputLeaseRelease(void);
BOOL AGInputWriteRequired(NSString *op);
ag_ios_result AGWithInputLease(ag_ios_result (^work)(void));

#ifdef __cplusplus
}
#endif
