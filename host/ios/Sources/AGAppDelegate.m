// AGAppDelegate.m
#import "AGAppDelegate.h"
#import "AGRootViewController.h"

@implementation AGAppDelegate

- (BOOL)application:(UIApplication *)application
didFinishLaunchingWithOptions:(NSDictionary<UIApplicationLaunchOptionsKey, id> *)launchOptions {
    self.window = [[UIWindow alloc] initWithFrame:UIScreen.mainScreen.bounds];
    AGRootViewController *root = [[AGRootViewController alloc] initWithNibName:nil bundle:nil];
    UINavigationController *nav = [[UINavigationController alloc] initWithRootViewController:root];
    self.window.rootViewController = nav;
    [self.window makeKeyAndVisible];
    return YES;
}

@end
