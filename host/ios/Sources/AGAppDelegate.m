// AGAppDelegate.m
#import "AGAppDelegate.h"
#import "AGRootViewController.h"
#import "AGAppCtlServer.h"

@implementation AGAppDelegate

- (BOOL)application:(UIApplication *)application
didFinishLaunchingWithOptions:(NSDictionary<UIApplicationLaunchOptionsKey, id> *)launchOptions {
    NSError *controlError=nil;
    if(![[AGAppCtlServer shared] start:&controlError])NSLog(@"[apkctl] 宿主通道启动失败: %@",controlError);
    self.window = [[UIWindow alloc] initWithFrame:UIScreen.mainScreen.bounds];
    AGRootViewController *root = [[AGRootViewController alloc] initWithNibName:nil bundle:nil];
    UINavigationController *nav = [[UINavigationController alloc] initWithRootViewController:root];
    self.window.rootViewController = nav;
    [self.window makeKeyAndVisible];
    return YES;
}

@end
