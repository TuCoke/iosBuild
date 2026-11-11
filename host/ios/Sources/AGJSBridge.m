#import "AGJSBridge.h"
#import "AGSDKLoader.h"
#import <UIKit/UIKit.h>
#include <dlfcn.h>
#include <math.h>

@protocol AGJSStructExport <JSExport>
@property(nonatomic,assign) double x,y,width,height;
@property(nonatomic,copy,readonly) NSString *kind;
- (NSDictionary *)json;
@end
// 使用有类型的结构对象，不把 CGRect 的内存布局或任意 ObjC 指针暴露给 JS。
@interface AGJSStruct : NSObject <AGJSStructExport>
@property(nonatomic,assign) double x,y,width,height;
@property(nonatomic,copy) NSString *kind;
@end
@implementation AGJSStruct
- (NSDictionary *)json {return @{@"kind":self.kind?:@"rect",@"x":@(self.x),@"y":@(self.y),@"width":@(self.width),@"height":@(self.height)};}
@end

@class AGJSSelector;
@interface AGJSActionTarget : NSObject
@property(nonatomic,weak) AGJSBridge *bridge;
@property(nonatomic,copy) NSString *identifier;
- (void)changed:(UIControl *)sender;
- (void)clicked:(UIControl *)sender;
@end
@protocol AGJSSelectorExport <JSExport>
@property(nonatomic,copy,readonly) NSString *identifier;
@property(nonatomic,strong,readonly) AGJSStruct *frame;
- (NSDictionary *)get;
- (void)set:(NSDictionary *)properties;
JSExportAs(on,- (void)on:(NSString *)event callback:(JSValue *)callback);
- (void)remove;
@end
@interface AGJSSelector : NSObject <AGJSSelectorExport>
@property(nonatomic,weak) AGJSBridge *bridge;
@property(nonatomic,copy) NSString *identifier;
@end

@interface AGJSBridge ()
@property(nonatomic,weak) JSContext *context;
@property(nonatomic,strong) dispatch_queue_t queue;
@property(nonatomic,strong) NSMutableDictionary<NSString *,UIView *> *views;
@property(nonatomic,strong) NSMutableDictionary<NSString *,AGJSActionTarget *> *targets;
@property(nonatomic,strong) NSMutableDictionary<NSString *,JSValue *> *callbacks;
@property(nonatomic,strong) NSMutableSet<NSString *> *matIDs;
@property(nonatomic,strong) UIWindow *window;
@property(nonatomic,weak) UIWindow *previousKeyWindow;
@property(nonatomic,assign) BOOL closed;
- (void)raise:(NSString *)message;
- (id)main:(id(^)(void))work;
- (NSDictionary *)properties:(NSString *)identifier;
- (void)configure:(NSDictionary *)properties identifier:(NSString *)identifier;
- (void)remove:(NSString *)identifier;
- (void)emit:(NSString *)event identifier:(NSString *)identifier;
@end

static UIColor *AGJSColor(id value) {
    if(![value isKindOfClass:NSString.class])return nil;
    NSString *text=[value stringByReplacingOccurrencesOfString:@"#" withString:@""];unsigned int n=0;
    if((text.length!=6 && text.length!=8) || ![[NSScanner scannerWithString:text] scanHexInt:&n])return nil;
    if(text.length==6)n=(n<<8)|255;
    return [UIColor colorWithRed:((n>>24)&255)/255.0 green:((n>>16)&255)/255.0 blue:((n>>8)&255)/255.0 alpha:(n&255)/255.0];
}
@implementation AGJSActionTarget
- (void)changed:(UIControl *)sender {[self.bridge emit:@"change" identifier:self.identifier];}
- (void)clicked:(UIControl *)sender {[self.bridge emit:@"click" identifier:self.identifier];}
@end
@implementation AGJSSelector
- (NSDictionary *)get {return [self.bridge properties:self.identifier];}
- (AGJSStruct *)frame {NSDictionary *p=[self get][@"frame"];AGJSStruct *out=[AGJSStruct new];out.kind=@"rect";out.x=[p[@"x"] doubleValue];out.y=[p[@"y"] doubleValue];out.width=[p[@"width"] doubleValue];out.height=[p[@"height"] doubleValue];return out;}
- (void)set:(NSDictionary *)properties {[self.bridge configure:properties identifier:self.identifier];}
- (void)on:(NSString *)event callback:(JSValue *)callback {
    if(![@[@"click",@"change"] containsObject:event]){[self.bridge raise:@"控件事件只支持 click/change"];return;}
    NSString *key=[NSString stringWithFormat:@"%@:%@",self.identifier,event];
    if(callback.isNull || callback.isUndefined)[self.bridge.callbacks removeObjectForKey:key];
    else if(callback.isObject && JSObjectIsFunction(callback.context.JSGlobalContextRef,JSValueToObject(callback.context.JSGlobalContextRef,callback.JSValueRef,NULL)))self.bridge.callbacks[key]=callback;
    else [self.bridge raise:@"事件回调必须为函数"];
}
- (void)remove {[self.bridge remove:self.identifier];}
@end

static bool AGJSAbort(JSContextRef context,void *data){return true;}
@implementation AGJSBridge
- (instancetype)initWithContext:(JSContext *)context queue:(dispatch_queue_t)queue {
    if((self=[super init])){_context=context;_queue=queue;_views=[NSMutableDictionary dictionary];_targets=[NSMutableDictionary dictionary];_callbacks=[NSMutableDictionary dictionary];_matIDs=[NSMutableSet set];}return self;
}
- (void)raise:(NSString *)message {self.context.exception=[JSValue valueWithNewErrorFromMessage:message inContext:self.context];}
// UIKit 操作在主线程，排队超时的任务取消；已开始的任务超时则明确要求核对结果。
- (id)main:(id(^)(void))work {
    if(NSThread.isMainThread){@try{return work();}@catch(NSException *error){[self raise:error.reason];return nil;}}
    NSCondition *condition=[NSCondition new];__block BOOL started=NO,finished=NO,abandoned=NO;__block id result=nil;__block NSString *failure=nil;
    dispatch_async(dispatch_get_main_queue(),^{
        [condition lock];if(abandoned){[condition unlock];return;}started=YES;[condition unlock];
        @try {result=work();}@catch(NSException *exception){failure=exception.reason;}
        [condition lock];finished=YES;[condition broadcast];[condition unlock];
    });
    [condition lock];NSDate *deadline=[NSDate dateWithTimeIntervalSinceNow:3];while(!finished && [condition waitUntilDate:deadline]){}
    BOOL done=finished,wasStarted=started;if(!done)abandoned=YES;id out=done?result:nil;NSString *error=done?failure:(wasStarted?@"UIKit 操作已开始但返回超时，请核对界面":@"UIKit 主线程排队超时，操作已取消");[condition unlock];
    if(error)[self raise:error];return out;
}
- (BOOL)ensureWindow {
    if(self.window)return YES;
    UIWindowScene *scene=nil;for(UIScene *candidate in UIApplication.sharedApplication.connectedScenes)if([candidate isKindOfClass:UIWindowScene.class] && candidate.activationState==UISceneActivationStateForegroundActive){scene=(UIWindowScene *)candidate;break;}
    if(!scene)return NO;
    for(UIWindow *window in scene.windows)if(window.isKeyWindow){self.previousKeyWindow=window;break;}
    self.window=[[UIWindow alloc] initWithWindowScene:scene];self.window.windowLevel=UIWindowLevelAlert-2;
    self.window.rootViewController=[UIViewController new];self.window.backgroundColor=UIColor.clearColor;
    self.window.rootViewController.view.backgroundColor=UIColor.systemBackgroundColor;[self.window makeKeyAndVisible];return YES;
}
- (void)install {
    __weak AGJSBridge *weakSelf=self;
    self.context[@"Rect"]=^id(double x,double y,double w,double h){AGJSStruct *rect=[AGJSStruct new];rect.kind=@"rect";rect.x=x;rect.y=y;rect.width=w;rect.height=h;return rect;};
    self.context[@"Point"]=^id(double x,double y){AGJSStruct *point=[AGJSStruct new];point.kind=@"point";point.x=x;point.y=y;return point;};
    self.context[@"Size"]=^id(double w,double h){AGJSStruct *size=[AGJSStruct new];size.kind=@"size";size.width=w;size.height=h;return size;};
    self.context[@"ui"]=@{};
    self.context[@"ui"][@"create"]=^id(NSString *type,NSString *identifier,NSDictionary *properties){
        AGJSBridge *bridge=weakSelf;if(!bridge || bridge.closed)return nil;
        if(![identifier isKindOfClass:NSString.class] || !identifier.length || identifier.length>128){[bridge raise:@"控件 ID 无效、重复或数量超过限制"];return nil;}
        NSDictionary *classes=@{@"view":UIView.class,@"label":UILabel.class,@"button":UIButton.class,@"input":UITextField.class,@"switch":UISwitch.class,@"slider":UISlider.class,@"text":UITextView.class,@"scroll":UIScrollView.class,@"stack":UIStackView.class};
        Class cls=classes[type];if(!cls){[bridge raise:@"不支持的原生控件类型"];return nil;}
        NSNumber *created=[bridge main:^id{
            if(bridge.views[identifier])@throw [NSException exceptionWithName:@"AGJSInvalid" reason:@"控件 ID 已存在" userInfo:nil];
            if(bridge.views.count>=256)@throw [NSException exceptionWithName:@"AGJSInvalid" reason:@"控件数量超过限制" userInfo:nil];
            if(![bridge ensureWindow])return @NO;
            UIView *view=[[cls alloc] initWithFrame:CGRectZero];view.accessibilityIdentifier=identifier;
            bridge.views[identifier]=view;
            if([view isKindOfClass:UIControl.class]){
                AGJSActionTarget *target=[AGJSActionTarget new];target.identifier=identifier;target.bridge=bridge;bridge.targets[identifier]=target;
                [(UIControl *)view addTarget:target action:@selector(clicked:) forControlEvents:UIControlEventTouchUpInside];
                [(UIControl *)view addTarget:target action:@selector(changed:) forControlEvents:UIControlEventValueChanged|UIControlEventEditingChanged];
            }
            [bridge.window.rootViewController.view addSubview:view];return @YES;
        }];
        if(!created.boolValue){if(!bridge.context.exception)[bridge raise:@"没有可用的前台 WindowScene"];return nil;}
        [bridge configure:properties identifier:identifier];AGJSSelector *selector=[AGJSSelector new];selector.bridge=bridge;selector.identifier=identifier;return selector;
    };
    self.context[@"ui"][@"get"]=^id(NSString *identifier){AGJSBridge *bridge=weakSelf;if(!bridge || bridge.closed)return nil;NSNumber *exists=[bridge main:^id{return @(bridge.views[identifier]!=nil);}];if(!exists.boolValue)return nil;AGJSSelector *selector=[AGJSSelector new];selector.bridge=bridge;selector.identifier=identifier;return selector;};
    self.context[@"ui"][@"clear"]=^{AGJSBridge *bridge=weakSelf;[bridge.callbacks removeAllObjects];[bridge main:^id{for(UIView *view in bridge.views.allValues)[view removeFromSuperview];[bridge.views removeAllObjects];[bridge.targets removeAllObjects];bridge.window.hidden=YES;bridge.window=nil;[bridge.previousKeyWindow makeKeyWindow];bridge.previousKeyWindow=nil;return @YES;}];};
    self.context[@"sdk"]=@{};
    self.context[@"sdk"][@"call"]=^id(NSString *method,NSDictionary *arguments){
        AGJSBridge *bridge=weakSelf;if(!bridge || bridge.closed)return nil;
        if(![method isKindOfClass:NSString.class] || !method.length || [method hasPrefix:@"script."] || [method hasPrefix:@"apkctl."] || ([method hasPrefix:@"sdk."] && ![method isEqualToString:@"sdk.status"])) {
            [bridge raise:@"此入口只调用设备能力；任务启动、升级和控制连接由 Go 脚本或宿主管理"];return nil;
        }
        if(arguments && ![arguments isKindOfClass:NSDictionary.class]){[bridge raise:@"SDK 参数必须为对象"];return nil;}
        BOOL cv=[method hasPrefix:@"opencv."];
        if(cv && ![method isEqualToString:@"opencv.decode"] && ![method isEqualToString:@"opencv.rgba"]) {
            if(![arguments[@"id"] isKindOfClass:NSString.class] || ![bridge.matIDs containsObject:arguments[@"id"]] || (arguments[@"template"] && ![bridge.matIDs containsObject:arguments[@"template"]])) {
                [bridge raise:@"Mat 不属于当前 JS 上下文或已关闭"];return nil;
            }
        }
        NSError *error=nil;NSData *payload=[NSJSONSerialization dataWithJSONObject:arguments?:@{} options:0 error:&error];
        if(!payload || payload.length>16*1024*1024){[bridge raise:error.localizedDescription?:@"SDK 参数过大"];return nil;}
        NSString *raw=[AGSDKLoader.shared invoke:method payload:[[NSString alloc] initWithData:payload encoding:NSUTF8StringEncoding]];
        NSDictionary *reply=raw?[NSJSONSerialization JSONObjectWithData:[raw dataUsingEncoding:NSUTF8StringEncoding] options:0 error:&error]:nil;
        if(![reply isKindOfClass:NSDictionary.class])reply=nil;
        if(!reply || ![reply[@"ok"] boolValue]){[bridge raise:reply[@"error"]?:AGSDKLoader.shared.lastError?:@"SDK 响应无效"];return nil;}
        id result=reply[@"data"];
        if(cv) {
            if([method isEqualToString:@"opencv.close"])[bridge.matIDs removeObject:arguments[@"id"]];
            if([result isKindOfClass:NSDictionary.class] && [result[@"id"] isKindOfClass:NSString.class])[bridge.matIDs addObject:result[@"id"]];
        }
        return result;
    };
}
- (NSDictionary *)properties:(NSString *)identifier {
    return [self main:^id{
        UIView *view=self.views[identifier];if(!view)return @{};
        CGRect f=view.frame;NSMutableDictionary *out=[@{@"id":identifier,@"hidden":@(view.hidden),@"frame":@{@"x":@(f.origin.x),@"y":@(f.origin.y),@"width":@(f.size.width),@"height":@(f.size.height)}} mutableCopy];
        if([view isKindOfClass:UILabel.class])out[@"text"]=[(UILabel *)view text]?:@"";
        if([view isKindOfClass:UITextField.class])out[@"text"]=[(UITextField *)view text]?:@"";
        if([view isKindOfClass:UITextView.class])out[@"text"]=[(UITextView *)view text]?:@"";
        if([view isKindOfClass:UIButton.class])out[@"text"]=[(UIButton *)view titleForState:UIControlStateNormal]?:@"";
        if([view isKindOfClass:UISwitch.class])out[@"value"]=@([(UISwitch *)view isOn]);
        if([view isKindOfClass:UISlider.class])out[@"value"]=@([(UISlider *)view value]);return out;
    }]?:@{};
}
- (void)configure:(NSDictionary *)properties identifier:(NSString *)identifier {
    if(![properties isKindOfClass:NSDictionary.class]){[self raise:@"控件属性必须为对象"];return;}
    // 不使用任意 KVC/performSelector，避免 JS 类型误配触发未定义调用约定。
    [self main:^id{
        UIView *view=self.views[identifier];if(!view)return @NO;
        id frame=properties[@"frame"];if([frame isKindOfClass:AGJSStruct.class])frame=[frame json];
        if([frame isKindOfClass:NSDictionary.class]){
            double x=[frame[@"x"] doubleValue],y=[frame[@"y"] doubleValue],w=[frame[@"width"] doubleValue],h=[frame[@"height"] doubleValue];
            if(!isfinite(x)||!isfinite(y)||!isfinite(w)||!isfinite(h)||w<0||h<0||w>10000||h>10000)@throw [NSException exceptionWithName:@"AGJSInvalid" reason:@"控件坐标无效" userInfo:nil];
            view.frame=CGRectMake(x,y,w,h);
        }
        if(properties[@"hidden"])view.hidden=[properties[@"hidden"] boolValue];
        UIColor *background=AGJSColor(properties[@"background"]);if(background)view.backgroundColor=background;
        id text=properties[@"text"];if([text isKindOfClass:NSString.class]){
            if([view isKindOfClass:UILabel.class])[(UILabel *)view setText:text];
            if([view isKindOfClass:UITextField.class])[(UITextField *)view setText:text];
            if([view isKindOfClass:UITextView.class])[(UITextView *)view setText:text];
            if([view isKindOfClass:UIButton.class])[(UIButton *)view setTitle:text forState:UIControlStateNormal];
        }
        if([view isKindOfClass:UIControl.class] && properties[@"enabled"])[(UIControl *)view setEnabled:[properties[@"enabled"] boolValue]];
        if([view isKindOfClass:UITextField.class] && [properties[@"placeholder"] isKindOfClass:NSString.class])[(UITextField *)view setPlaceholder:properties[@"placeholder"]];
        if([view isKindOfClass:UISwitch.class] && properties[@"value"])[(UISwitch *)view setOn:[properties[@"value"] boolValue]];
        if([view isKindOfClass:UISlider.class] && properties[@"value"])[(UISlider *)view setValue:[properties[@"value"] floatValue]];
        NSString *parent=properties[@"parent"];if([parent isKindOfClass:NSString.class]){
            UIView *container=self.views[parent];if(!container || container==view || [container isDescendantOfView:view])@throw [NSException exceptionWithName:@"AGJSInvalid" reason:@"父控件无效或形成循环" userInfo:nil];
            if([container isKindOfClass:UIStackView.class])[(UIStackView *)container addArrangedSubview:view];else [container addSubview:view];
        }
        return @YES;
    }];
}
- (void)remove:(NSString *)identifier {
    NSArray *removed=[self main:^id{
        UIView *root=self.views[identifier];if(!root)return @[];
        NSMutableArray *ids=[NSMutableArray array];for(NSString *key in self.views.allKeys){UIView *view=self.views[key];if(view==root || [view isDescendantOfView:root])[ids addObject:key];}
        [root removeFromSuperview];for(NSString *key in ids){[self.views removeObjectForKey:key];[self.targets removeObjectForKey:key];}return ids;
    }];
    for(NSString *key in removed){[self.callbacks removeObjectForKey:[key stringByAppendingString:@":click"]];[self.callbacks removeObjectForKey:[key stringByAppendingString:@":change"]];}
}
- (void)emit:(NSString *)event identifier:(NSString *)identifier {
    // UIKit 不等待 JS，避免主线程与脚本队列互相等待。
    dispatch_async(self.queue,^{
        if(self.closed)return;JSValue *callback=self.callbacks[[NSString stringWithFormat:@"%@:%@",identifier,event]];if(!callback)return;
        typedef void(*SetLimit)(JSContextGroupRef,double,bool(*)(JSContextRef,void *),void *);typedef void(*ClearLimit)(JSContextGroupRef);
        SetLimit set=(SetLimit)dlsym(RTLD_DEFAULT,"JSContextGroupSetExecutionTimeLimit");ClearLimit clear=(ClearLimit)dlsym(RTLD_DEFAULT,"JSContextGroupClearExecutionTimeLimit");
        if(!set || !clear){[self raise:@"缺少 JS 回调执行限额入口"];return;}
        NSDictionary *payload=[self properties:identifier];JSContextGroupRef group=JSContextGetGroup(self.context.JSGlobalContextRef);self.context.exception=nil;
        set(group,5,AGJSAbort,NULL);
        @try {[callback callWithArguments:@[@{@"id":identifier,@"event":event,@"properties":payload}]];}
        @catch(NSException *error){[self raise:error.reason];}
        @finally {clear(group);}
        if(self.context.exception)NSLog(@"[agov] JS 控件回调失败: %@",self.context.exception);
    });
}
- (void)close {
    self.closed=YES;[self.callbacks removeAllObjects];
    for(NSString *identifier in self.matIDs){
        NSData *data=[NSJSONSerialization dataWithJSONObject:@{@"id":identifier} options:0 error:nil];
        [AGSDKLoader.shared invoke:@"opencv.close" payload:[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding]];
    }
    [self.matIDs removeAllObjects];
    // 清理不阻塞关闭连接；block 持有 bridge 到主线程清理完成。
    dispatch_async(dispatch_get_main_queue(),^{for(UIView *view in self.views.allValues)[view removeFromSuperview];[self.views removeAllObjects];[self.targets removeAllObjects];self.window.hidden=YES;self.window=nil;[self.previousKeyWindow makeKeyWindow];self.previousKeyWindow=nil;});
}
@end
