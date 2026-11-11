#import <UIKit/UIKit.h>

// 穿透参数仅属于宿主配置，不改变 SDK 的连接接口。
@interface AGTunnelSettingsViewController : UITableViewController
+ (NSDictionary *)configurationWithError:(NSError **)error;
+ (BOOL)validateConfiguration:(NSDictionary *)config error:(NSError **)error;
@end
