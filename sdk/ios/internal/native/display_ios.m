//go:build ios && cgo

#import "runtime.h"
#include <stdatomic.h>

static id orientationObserver;
static atomic_int orientation;
static int AGQuarterTurns(NSInteger value){switch(value){case 2:return 2;case 3:return 3;case 4:return 1;default:return 0;}}

int AGScreenRotation(void){return atomic_load(&orientation);}

// 参考样本通过 FBSOrientationObserver 观察前台方向；不能把宿主 App 的方向当作设备方向。
ag_ios_result AGDisplayInfo(void){
 return AGOnMain(^{
    static dispatch_once_t once;
    dispatch_once(&once,^{
        dlopen("/System/Library/PrivateFrameworks/FrontBoardServices.framework/FrontBoardServices",RTLD_LAZY|RTLD_GLOBAL);
        Class cls=NSClassFromString(@"FBSOrientationObserver");
        if(cls){orientationObserver=[[cls alloc] init];SEL set=NSSelectorFromString(@"setHandler:");
            if([orientationObserver respondsToSelector:set]){
                void (^handler)(id)=^(id update){SEL get=NSSelectorFromString(@"orientation");if([update respondsToSelector:get])atomic_store(&orientation,AGQuarterTurns(((NSInteger(*)(id,SEL))objc_msgSend)(update,get)));};
                ((void(*)(id,SEL,id))objc_msgSend)(orientationObserver,set,handler);
            }
        }
    });
    SEL get=NSSelectorFromString(@"activeInterfaceOrientation");
    if([orientationObserver respondsToSelector:get])atomic_store(&orientation,AGQuarterTurns(((NSInteger(*)(id,SEL))objc_msgSend)(orientationObserver,get)));
    UIScreen *screen=UIScreen.mainScreen;CGRect native=screen.nativeBounds;
    if(CGRectIsEmpty(native) || screen.scale<=0)return AGError(3,@"屏幕几何信息不可用");
    return AGJSON(@{@"width":@(native.size.width),@"height":@(native.size.height),@"scale":@(screen.scale),@"rotation":@(atomic_load(&orientation)),@"orientationAvailable":@([orientationObserver respondsToSelector:get])});
 });
}
