#import <UIKit/UIKit.h>
NS_ASSUME_NONNULL_BEGIN
// iOS Agent 使用自己的云控凭据，不使用 FRP 或调试接口 token。
@interface AGAgentSettingsViewController : UITableViewController
+ (nullable NSDictionary *)configurationWithError:(NSError **)error;
+ (void)startSavedIfEnabled;
@end
NS_ASSUME_NONNULL_END
