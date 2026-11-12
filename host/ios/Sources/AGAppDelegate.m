// AGAppDelegate.m
#import "AGAppDelegate.h"
#import "AGRootViewController.h"
#import "AGAppCtlServer.h"
#import "AGBackgroundRuntime.h"

@implementation AGAppDelegate

- (BOOL)application:(UIApplication *)application
didFinishLaunchingWithOptions:(NSDictionary<UIApplicationLaunchOptionsKey, id> *)launchOptions {
    [[AGBackgroundRuntime shared] install];
    NSError *controlError=nil;
    if(![[AGAppCtlServer shared] start:&controlError])NSLog(@"[apkctl] 宿主通道启动失败: %@",controlError);
    self.window = [[UIWindow alloc] initWithFrame:UIScreen.mainScreen.bounds];
    AGRootViewController *root = [[AGRootViewController alloc] initWithNibName:nil bundle:nil];
    UINavigationController *nav = [[UINavigationController alloc] initWithRootViewController:root];
    self.window.rootViewController = nav;
    [self.window makeKeyAndVisible];
    return YES;
}

- (void)applicationWillTerminate:(UIApplication *)application {
    [[AGBackgroundRuntime shared] command:@"terminate" owner:@"host" enabled:NO];
}

@end
