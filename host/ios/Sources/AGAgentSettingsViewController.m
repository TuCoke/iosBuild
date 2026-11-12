#import "AGAgentSettingsViewController.h"
#import "AGBridge.h"
#import <Security/Security.h>
static NSString *const AGAgentDefaults=@"agov.ios.agent.configuration.v1";
static NSMutableDictionary *AGAgentTokenQuery(void){return [@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,(__bridge id)kSecAttrService:@"dev.autogov.host.agent",(__bridge id)kSecAttrAccount:@"cloud-token"} mutableCopy];}
@interface AGAgentSettingsViewController ()
@property(nonatomic,strong) UITextField *serverField;
@property(nonatomic,strong) UITextField *tokenField;
@property(nonatomic,strong) UISwitch *insecureSwitch;
@property(nonatomic,strong) UISwitch *autoSwitch;
@property(nonatomic,strong) UILabel *stateLabel;
@property(nonatomic,assign) BOOL busy;
@property(nonatomic,assign) BOOL readable;
@end
@implementation AGAgentSettingsViewController
+ (NSDictionary *)configurationWithError:(NSError **)error {
 NSMutableDictionary *config=[[[NSUserDefaults standardUserDefaults] dictionaryForKey:AGAgentDefaults] mutableCopy] ?: [NSMutableDictionary dictionary];
 NSMutableDictionary *q=AGAgentTokenQuery();q[(__bridge id)kSecReturnData]=@YES;CFTypeRef value=NULL;
 OSStatus code=SecItemCopyMatching((__bridge CFDictionaryRef)q,&value);
 if(code!=errSecSuccess&&code!=errSecItemNotFound){if(error)*error=[NSError errorWithDomain:@"AgovAgent" code:code userInfo:@{NSLocalizedDescriptionKey:@"读取 Agent 凭据失败，配置未覆盖"}];return nil;}
 config[@"token"]=value?[[NSString alloc] initWithData:CFBridgingRelease(value) encoding:NSUTF8StringEncoding] ?: @"":@"";
 return config;
}
+ (void)startSavedIfEnabled {
 dispatch_async(dispatch_get_global_queue(QOS_CLASS_UTILITY,0),^{
  NSError *error=nil;NSDictionary *config=[self configurationWithError:&error];if(!config||![config[@"autoConnect"] boolValue])return;
  NSData *data=[NSJSONSerialization dataWithJSONObject:config options:0 error:NULL];
  NSString *raw=[AGBridge invoke:@"agent.start" payload:[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding]];
  NSDictionary *reply=raw?[NSJSONSerialization JSONObjectWithData:[raw dataUsingEncoding:NSUTF8StringEncoding] options:0 error:NULL]:nil;
  // 不输出配置或凭据；连接详情通过 Agent 状态页查看。
  NSLog(@"[ios-agent] 自动连接请求%@",[reply[@"ok"] boolValue]?@"已受理":@"失败，请检查 Agent 设置");
 });
}
- (void)viewDidLoad {
 [super viewDidLoad];self.title=@"iOS Agent";self.tableView.keyboardDismissMode=UIScrollViewKeyboardDismissModeOnDrag;
 self.navigationItem.leftBarButtonItem=[[UIBarButtonItem alloc] initWithTitle:@"返回" style:UIBarButtonItemStylePlain target:self action:@selector(closePage)];
 self.navigationItem.rightBarButtonItem=[[UIBarButtonItem alloc] initWithTitle:@"保存" style:UIBarButtonItemStyleDone target:self action:@selector(savePage)];
 NSError *error=nil;NSDictionary *config=[AGAgentSettingsViewController configurationWithError:&error];self.readable=config!=nil;
 self.serverField=[UITextField new];self.serverField.placeholder=@"wss://云控地址/agent";self.serverField.text=config[@"server"] ?: @"";self.serverField.keyboardType=UIKeyboardTypeURL;
 self.tokenField=[UITextField new];self.tokenField.placeholder=@"云控 Agent 卡密，不是 FRP token";self.tokenField.text=config[@"token"] ?: @"";self.tokenField.secureTextEntry=YES;
 for(UITextField *f in @[self.serverField,self.tokenField]){f.autocorrectionType=UITextAutocorrectionTypeNo;f.autocapitalizationType=UITextAutocapitalizationTypeNone;f.clearButtonMode=UITextFieldViewModeWhileEditing;f.font=[UIFont systemFontOfSize:14];}
 self.insecureSwitch=[UISwitch new];self.insecureSwitch.on=[config[@"allowInsecure"] boolValue];self.autoSwitch=[UISwitch new];self.autoSwitch.on=[config[@"autoConnect"] boolValue];
 self.stateLabel=[UILabel new];self.stateLabel.numberOfLines=0;self.stateLabel.font=[UIFont systemFontOfSize:14];self.stateLabel.text=@"正在查询状态…";
 self.navigationItem.rightBarButtonItem.enabled=self.readable;
 if(error)[self message:error.localizedDescription];[self refreshStatus];
}
- (void)closePage {[self dismissViewControllerAnimated:YES completion:nil];}
- (void)message:(NSString *)text {UIAlertController *a=[UIAlertController alertControllerWithTitle:@"iOS Agent" message:text preferredStyle:UIAlertControllerStyleAlert];[a addAction:[UIAlertAction actionWithTitle:@"知道了" style:UIAlertActionStyleDefault handler:nil]];[self presentViewController:a animated:YES completion:nil];}
- (NSDictionary *)draft {
 return @{@"server":[self.serverField.text stringByTrimmingCharactersInSet:NSCharacterSet.whitespaceAndNewlineCharacterSet] ?: @"",@"token":self.tokenField.text ?: @"",@"allowInsecure":@(self.insecureSwitch.on),@"autoConnect":@(self.autoSwitch.on)};
}
- (BOOL)saveConfiguration {
 if(!self.readable){[self message:@"凭据读取失败，禁止覆盖原有配置"];return NO;}
 NSDictionary *c=[self draft];NSURLComponents *u=[NSURLComponents componentsWithString:c[@"server"]];
 if(!u.host.length||![@[@"ws",@"wss"] containsObject:u.scheme]||![u.path isEqualToString:@"/agent"]||u.user.length||u.password.length||u.query.length||u.fragment.length){[self message:@"地址请填写 ws(s)://主机/agent，不能包含用户名、密码或查询参数"];return NO;}
 BOOL loop=[@[@"localhost",@"127.0.0.1",@"::1"] containsObject:u.host];
 if(!loop&&[u.scheme isEqualToString:@"ws"]&&!self.insecureSwitch.on){[self message:@"公网默认使用 WSS。内网明文联调需主动打开对应开关"];return NO;}
 if(!loop&&![c[@"token"] length]){[self message:@"请填写云控 Agent 卡密，它不是 FRP token 或调试口令"];return NO;}
 NSMutableDictionary *q=AGAgentTokenQuery();NSDictionary *attr=@{(__bridge id)kSecValueData:[c[@"token"] dataUsingEncoding:NSUTF8StringEncoding],(__bridge id)kSecAttrAccessible:(__bridge id)kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly};
 OSStatus code=SecItemUpdate((__bridge CFDictionaryRef)q,(__bridge CFDictionaryRef)attr);
 if(code==errSecItemNotFound){[q addEntriesFromDictionary:attr];code=SecItemAdd((__bridge CFDictionaryRef)q,NULL);}
 if(code!=errSecSuccess){[self message:@"Keychain 保存失败，配置未保存"];return NO;}
 NSMutableDictionary *public=[c mutableCopy];[public removeObjectForKey:@"token"];[NSUserDefaults.standardUserDefaults setObject:public forKey:AGAgentDefaults];return YES;
}
- (void)savePage {if([self saveConfiguration])[self message:@"已保存。若 Agent 正在运行，请先停止，再连接以应用新配置。"];}
- (void)call:(NSString *)method payload:(NSDictionary *)payload {
 if(self.busy)return;self.busy=YES;[self.tableView reloadData];
 dispatch_async(dispatch_get_global_queue(QOS_CLASS_UTILITY,0),^{
  NSData *data=[NSJSONSerialization dataWithJSONObject:payload ?: @{} options:0 error:NULL];NSString *raw=[AGBridge invoke:method payload:[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding]];
  NSDictionary *reply=raw?[NSJSONSerialization JSONObjectWithData:[raw dataUsingEncoding:NSUTF8StringEncoding] options:0 error:NULL]:nil;
  dispatch_async(dispatch_get_main_queue(),^{self.busy=NO;
   if(![reply[@"ok"] boolValue]){self.stateLabel.text=reply[@"error"] ?: @"SDK 返回失败，请确认安装了支持 Agent 的构建";}
   else {NSDictionary *d=reply[@"data"];NSDictionary *names=@{@"online":@"在线",@"connecting":@"连接中",@"reconnecting":@"等待重连",@"disconnected":@"连接已断开",@"stopping":@"正在清理",@"stopped":@"已停止",@"cleanup_failed":@"清理未确认，请检查现场"};self.stateLabel.text=[NSString stringWithFormat:@"%@\n%@",names[d[@"state"]] ?: @"未启动",d[@"lastError"] ?: @""];}
   [self.tableView reloadData];
  });
 });
}
- (void)refreshStatus {[self call:@"agent.status" payload:@{}];}
- (NSInteger)numberOfSectionsInTableView:(UITableView *)t{return 3;}
- (NSInteger)tableView:(UITableView *)t numberOfRowsInSection:(NSInteger)s{return s==0?4:s==1?3:1;}
- (NSString *)tableView:(UITableView *)t titleForHeaderInSection:(NSInteger)s{return @[@"云控连接",@"运行控制",@"实际状态"][s];}
- (NSString *)tableView:(UITableView *)t titleForFooterInSection:(NSInteger)s{return s==0?@"iOS 使用自己的 Agent 直接连接云控，安卓设备可同时在线。凭据保存在本机 Keychain。默认要求 WSS；自动连接只在打开宿主加载 SDK 后触发，不等于系统开机自启动。":s==1?@"首次从电脑联调桥迁移时，请先停止旧桥，避免同一手机重复注册。停止后要等实际清理完成。":@"点击刷新获取服务端注册后的真实状态；连接中不等于已上线。";}
- (CGFloat)tableView:(UITableView *)t heightForRowAtIndexPath:(NSIndexPath *)p{return p.section==2?90:70;}
- (UITableViewCell *)tableView:(UITableView *)t cellForRowAtIndexPath:(NSIndexPath *)p {
 UITableViewCell *cell=[[UITableViewCell alloc] initWithStyle:UITableViewCellStyleDefault reuseIdentifier:nil];cell.selectionStyle=UITableViewCellSelectionStyleNone;
 if(p.section==0){if(p.row>=2){cell.textLabel.text=p.row==2?@"允许内网明文联调":@"打开宿主后自动连接";cell.accessoryView=p.row==2?self.insecureSwitch:self.autoSwitch;return cell;}
  UILabel *label=[UILabel new];label.text=p.row==0?@"服务端地址":@"Agent 凭据";label.font=[UIFont systemFontOfSize:13];UIStackView *stack=[[UIStackView alloc] initWithArrangedSubviews:@[label,p.row==0?self.serverField:self.tokenField]];stack.axis=UILayoutConstraintAxisVertical;stack.spacing=5;stack.translatesAutoresizingMaskIntoConstraints=NO;[cell.contentView addSubview:stack];[NSLayoutConstraint activateConstraints:@[[stack.leadingAnchor constraintEqualToAnchor:cell.contentView.leadingAnchor constant:16],[stack.trailingAnchor constraintEqualToAnchor:cell.contentView.trailingAnchor constant:-16],[stack.centerYAnchor constraintEqualToAnchor:cell.contentView.centerYAnchor]]];
 }else if(p.section==1){cell.textLabel.text=@[@"连接云控",@"停止并清理",@"刷新状态"][p.row];cell.textLabel.textColor=self.busy?UIColor.secondaryLabelColor:UIColor.systemBlueColor;cell.selectionStyle=UITableViewCellSelectionStyleDefault;
 }else {self.stateLabel.translatesAutoresizingMaskIntoConstraints=NO;[cell.contentView addSubview:self.stateLabel];[NSLayoutConstraint activateConstraints:@[[self.stateLabel.leadingAnchor constraintEqualToAnchor:cell.contentView.leadingAnchor constant:16],[self.stateLabel.trailingAnchor constraintEqualToAnchor:cell.contentView.trailingAnchor constant:-16],[self.stateLabel.centerYAnchor constraintEqualToAnchor:cell.contentView.centerYAnchor]]];}
 return cell;
}
- (void)tableView:(UITableView *)t didSelectRowAtIndexPath:(NSIndexPath *)p {
 [t deselectRowAtIndexPath:p animated:YES];if(p.section!=1||self.busy)return;
 if(p.row==0){if([self saveConfiguration])[self call:@"agent.start" payload:[self draft]];}
 else if(p.row==1)[self call:@"agent.stop" payload:@{}];else [self refreshStatus];
}
@end
