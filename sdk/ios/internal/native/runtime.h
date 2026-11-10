#import <Foundation/Foundation.h>
#import <UIKit/UIKit.h>
#import <CoreGraphics/CoreGraphics.h>
#import <dlfcn.h>
#import <objc/message.h>
#include <math.h>
#include "bridge.h"

ag_ios_result AGError(int code, NSString *message);
ag_ios_result AGData(NSData *data);
ag_ios_result AGJSON(id object);
ag_ios_result AGOnMain(ag_ios_result (^work)(void));
void *AGSymbol(const char *name);
void AGLoadFrameworks(void);
ag_ios_result AGDevice(NSString *op, NSDictionary *args);
ag_ios_result AGApplication(NSString *op, NSDictionary *args);
ag_ios_result AGInput(NSString *op, NSDictionary *args);
BOOL AGInputAvailable(NSString *kind);
ag_ios_result AGCapture(NSDictionary *args);
// 返回独立拥有的图像，调用者必须 CGImageRelease；采集资源在内部串行保护。
CGImageRef AGCopyScreenImage(int width, int height, NSString **error);
void AGReleaseCaptureSurface(void);
void AGDrawImage(CGContextRef context, CGImageRef image, int width, int height, int rotation);
ag_ios_result AGVideo(NSString *op, NSDictionary *args);
ag_ios_result AGSystem(NSString *op, NSDictionary *args);
ag_ios_result AGUtils(NSString *op, NSDictionary *args);
ag_ios_result AGUIAcc(NSString *op, NSDictionary *args);
ag_ios_result AGOverlay(NSString *op, NSDictionary *args);
ag_ios_result AGAPKCtl(NSString *op, NSDictionary *args);
ag_ios_result AGSelfTest(NSString *op, NSDictionary *args);
// 悬浮层（imgui + Metal）：hud.* / console.* 的实现
ag_ios_result AGImGuiOverlay(NSString *op, NSDictionary *args);
BOOL AGImGuiOverlayAvailable(void);
