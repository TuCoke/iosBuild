#import "AGTunnelSettingsViewController.h"
#import <Security/Security.h>

static NSString *const AGTunnelDefaultsKey = @"agov.tunnel.settings";
static NSString *const AGTunnelKeychainService = @"dev.autogov.host.frp";

static BOOL AGTunnelFail(NSError **error, NSString *message) {
    if(error)*error=[NSError errorWithDomain:@"AgovTunnelSettings" code:1 userInfo:@{NSLocalizedDescriptionKey:message}];
    return NO;
}
static NSMutableDictionary *AGTunnelTokenQuery(void) {
    return [@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,
              (__bridge id)kSecAttrService:AGTunnelKeychainService,
              (__bridge id)kSecAttrAccount:@"frps-token"} mutableCopy];
}

@interface AGTunnelSettingsViewController ()
@property (nonatomic, strong) NSDictionary<NSString *, UITextField *> *fields;
@property (nonatomic, strong) UISwitch *tlsSwitch;
@end

@implementation AGTunnelSettingsViewController

+ (NSDictionary *)configurationWithError:(NSError **)error {
    NSMutableDictionary *config=[@{@"server":@"49.235.187.13",@"serverPort":@7000,
                                   @"localPort":@18989,@"remotePort":@18989,
                                   @"name":@"agov-iphone",@"disableTls":@NO} mutableCopy];
    NSDictionary *saved=[[NSUserDefaults standardUserDefaults] dictionaryForKey:AGTunnelDefaultsKey];
    if(saved)[config addEntriesFromDictionary:saved];
    // token 与普通参数分开保存；不把服务器密钥写入源码或普通偏好文件。
    NSMutableDictionary *query=AGTunnelTokenQuery();
    query[(__bridge id)kSecReturnData]=@YES;
    query[(__bridge id)kSecMatchLimit]=(__bridge id)kSecMatchLimitOne;
    CFTypeRef result=NULL;
    OSStatus status=SecItemCopyMatching((__bridge CFDictionaryRef)query,&result);
    if(status==errSecSuccess){
        NSData *data=CFBridgingRelease(result);
        config[@"token"]=[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding] ?: @"";
    }else if(status==errSecItemNotFound){
        // 仅首次无已保存密钥、且仍使用对应服务器时使用构建预填值。
        // 构建密钥由 CI 注入安装包，不在 Git 中保存；用户保存后的 Keychain 值优先。
        id preset=NSBundle.mainBundle.infoDictionary[@"AGDefaultFrpsToken"];
        BOOL matching=[config[@"server"] isEqualToString:@"49.235.187.13"] && [config[@"serverPort"] integerValue]==7000;
        config[@"token"]=(matching && [preset isKindOfClass:NSString.class]) ? preset : @"";
    }
    else {AGTunnelFail(error,[NSString stringWithFormat:@"读取 frps 密钥失败（Keychain %d）",(int)status]);return nil;}
    return config;
}

+ (BOOL)validateConfiguration:(NSDictionary *)config error:(NSError **)error {
    NSString *server=config[@"server"];
    if(![server isKindOfClass:NSString.class]||server.length==0||[server containsString:@"://"]||
       [server rangeOfCharacterFromSet:[NSCharacterSet whitespaceAndNewlineCharacterSet]].location!=NSNotFound||
       [server rangeOfCharacterFromSet:[NSCharacterSet characterSetWithCharactersInString:@"/?#"]].location!=NSNotFound)
        return AGTunnelFail(error,@"服务器请只填写 IP 或域名，不要包含 http://、端口或路径");
    for(NSString *key in @[@"serverPort",@"localPort",@"remotePort"]){
        id value=config[key];
        if(![value isKindOfClass:NSNumber.class]||[value doubleValue] != [value integerValue]||[value integerValue]<1||[value integerValue]>65535)
            return AGTunnelFail(error,@"端口必须是 1～65535 的整数");
    }
    NSString *name=config[@"name"],*token=config[@"token"];
    if(![name isKindOfClass:NSString.class]||name.length==0||name.length>100)
        return AGTunnelFail(error,@"请填写隧道名称，最多 100 个字符；多台手机需使用不同名称和公网端口");
    if(![token isKindOfClass:NSString.class]||token.length==0)
        return AGTunnelFail(error,@"请填写 frps 服务器鉴权 token；它不是调试接口的 1234567");
    return YES;
}

- (void)viewDidLoad {
    [super viewDidLoad];
    self.title=@"穿透设置";
    self.tableView.keyboardDismissMode=UIScrollViewKeyboardDismissModeOnDrag;
    self.navigationItem.leftBarButtonItem=[[UIBarButtonItem alloc] initWithTitle:@"取消" style:UIBarButtonItemStylePlain target:self action:@selector(closeSettings)];
    self.navigationItem.rightBarButtonItem=[[UIBarButtonItem alloc] initWithTitle:@"保存" style:UIBarButtonItemStyleDone target:self action:@selector(saveSettings)];
    NSError *error=nil;
    NSDictionary *config=[AGTunnelSettingsViewController configurationWithError:&error];
    // 读取失败时禁止覆盖，不能把临时的空值保存成丢失的配置。
    self.navigationItem.rightBarButtonItem.enabled=(config!=nil);
    NSMutableDictionary *fields=[NSMutableDictionary dictionary];
    for(NSString *key in @[@"server",@"serverPort",@"token",@"localPort",@"remotePort",@"name"]){
        UITextField *field=[[UITextField alloc] init];
        field.text=[config[key] description] ?: @"";
        field.autocorrectionType=UITextAutocorrectionTypeNo;
        field.autocapitalizationType=UITextAutocapitalizationTypeNone;
        field.clearButtonMode=UITextFieldViewModeWhileEditing;
        field.font=[UIFont systemFontOfSize:15];
        field.accessibilityIdentifier=[@"tunnel." stringByAppendingString:key];
        field.secureTextEntry=[key isEqualToString:@"token"];
        if([key isEqualToString:@"token"]){
            UIButton *preset=[UIButton buttonWithType:UIButtonTypeSystem];
            [preset setTitle:@"使用预设" forState:UIControlStateNormal];
            preset.titleLabel.font=[UIFont systemFontOfSize:13];
            preset.frame=CGRectMake(0,0,80,30);
            [preset addTarget:self action:@selector(usePresetToken) forControlEvents:UIControlEventTouchUpInside];
            field.rightView=preset;field.rightViewMode=UITextFieldViewModeAlways;
            field.clearButtonMode=UITextFieldViewModeNever;
        }
        if([key hasSuffix:@"Port"])field.keyboardType=UIKeyboardTypeNumberPad;
        else if([key isEqualToString:@"server"])field.keyboardType=UIKeyboardTypeURL;
        fields[key]=field;
    }
    self.fields=fields;
    self.tlsSwitch=[[UISwitch alloc] init];self.tlsSwitch.on=![config[@"disableTls"] boolValue];
    if(error)dispatch_async(dispatch_get_main_queue(),^{[self showMessage:error.localizedDescription];});
}

- (NSInteger)numberOfSectionsInTableView:(UITableView *)tableView { return 2; }
- (NSInteger)tableView:(UITableView *)tableView numberOfRowsInSection:(NSInteger)section { return section==0 ? 4 : 3; }
- (NSString *)tableView:(UITableView *)tableView titleForHeaderInSection:(NSInteger)section { return section==0 ? @"公网 frps 服务端" : @"手机端口映射"; }
- (NSString *)tableView:(UITableView *)tableView titleForFooterInSection:(NSInteger)section {
    return section==0 ? @"填写部署 frps 的服务器地址与鉴权 token。TLS 默认开启。token 保存在本机 Keychain 中。" : @"调试接口使用手机本地端口 18990。公网端口需在服务器安全组放行；多台手机不能占用同一公网端口。保存后返回主界面点击启动穿透，已运行的隧道需先停止再启动。";
}
- (CGFloat)tableView:(UITableView *)tableView heightForRowAtIndexPath:(NSIndexPath *)indexPath { return 72; }
- (UITableViewCell *)tableView:(UITableView *)tableView cellForRowAtIndexPath:(NSIndexPath *)indexPath {
    UITableViewCell *cell=[[UITableViewCell alloc] initWithStyle:UITableViewCellStyleDefault reuseIdentifier:nil];
    cell.selectionStyle=UITableViewCellSelectionStyleNone;
    if(indexPath.section==0 && indexPath.row==3){cell.textLabel.text=@"TLS 加密连接";cell.accessoryView=self.tlsSwitch;return cell;}
    NSArray *keys=indexPath.section==0 ? @[@"server",@"serverPort",@"token"] : @[@"localPort",@"remotePort",@"name"];
    NSArray *titles=indexPath.section==0 ? @[@"服务器 IP / 域名",@"连接端口",@"frps 鉴权 token"] : @[@"手机本地端口",@"公网映射端口",@"隧道名称"];
    UIStackView *stack=[[UIStackView alloc] initWithArrangedSubviews:@[[self label:titles[indexPath.row]],self.fields[keys[indexPath.row]]]];
    stack.axis=UILayoutConstraintAxisVertical;stack.spacing=5;stack.translatesAutoresizingMaskIntoConstraints=NO;
    [cell.contentView addSubview:stack];
    [NSLayoutConstraint activateConstraints:@[[stack.leadingAnchor constraintEqualToAnchor:cell.contentView.layoutMarginsGuide.leadingAnchor],
        [stack.trailingAnchor constraintEqualToAnchor:cell.contentView.layoutMarginsGuide.trailingAnchor],
        [stack.topAnchor constraintEqualToAnchor:cell.contentView.topAnchor constant:8],
        [stack.bottomAnchor constraintEqualToAnchor:cell.contentView.bottomAnchor constant:-8]]];
    return cell;
}
- (UILabel *)label:(NSString *)text {UILabel *label=[[UILabel alloc] init];label.text=text;label.font=[UIFont systemFontOfSize:12];label.textColor=UIColor.secondaryLabelColor;return label;}
- (void)closeSettings {[self dismissViewControllerAnimated:YES completion:nil];}
- (void)usePresetToken {
    // 仅填写当前表单，点击保存后才替换 Keychain；不影响正在运行的连接。
    NSString *server=[self.fields[@"server"].text stringByTrimmingCharactersInSet:NSCharacterSet.whitespaceAndNewlineCharacterSet];
    if(![server isEqualToString:@"49.235.187.13"] || ![self.fields[@"serverPort"].text isEqualToString:@"7000"]){
        [self showMessage:@"此预设属于 49.235.187.13:7000，请先确认服务器地址和端口"];return;
    }
    id token=NSBundle.mainBundle.infoDictionary[@"AGDefaultFrpsToken"];
    if(![token isKindOfClass:NSString.class] || ![token length]){[self showMessage:@"当前安装包没有预设密钥，请使用包含预填配置的新包"];return;}
    self.fields[@"token"].text=token;
    [self showMessage:@"已填入服务器预设 token，请点保存后停止并重新启动穿透"];
}
- (void)showMessage:(NSString *)message {
    UIAlertController *alert=[UIAlertController alertControllerWithTitle:@"穿透设置" message:message preferredStyle:UIAlertControllerStyleAlert];
    [alert addAction:[UIAlertAction actionWithTitle:@"确定" style:UIAlertActionStyleDefault handler:nil]];
    [self presentViewController:alert animated:YES completion:nil];
}
- (void)saveSettings {
    [self.view endEditing:YES];
    NSMutableDictionary *config=[NSMutableDictionary dictionary];
    NSCharacterSet *space=NSCharacterSet.whitespaceAndNewlineCharacterSet;
    for(NSString *key in self.fields){
        NSString *text=[self.fields[key].text stringByTrimmingCharactersInSet:space] ?: @"";
        if([key hasSuffix:@"Port"]){
            if(text.length==0||[text rangeOfCharacterFromSet:NSCharacterSet.decimalDigitCharacterSet.invertedSet].location!=NSNotFound){[self showMessage:@"端口必须填写整数"];return;}
            config[key]=@([text integerValue]);
        }else config[key]=text;
    }
    config[@"disableTls"]=@(!self.tlsSwitch.on);
    NSError *error=nil;
    if(![AGTunnelSettingsViewController validateConfiguration:config error:&error]){[self showMessage:error.localizedDescription];return;}
    NSMutableDictionary *query=AGTunnelTokenQuery();
    NSDictionary *attributes=@{(__bridge id)kSecValueData:[config[@"token"] dataUsingEncoding:NSUTF8StringEncoding],
                               (__bridge id)kSecAttrAccessible:(__bridge id)kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly};
    OSStatus status=SecItemUpdate((__bridge CFDictionaryRef)query,(__bridge CFDictionaryRef)attributes);
    if(status==errSecItemNotFound){[query addEntriesFromDictionary:attributes];status=SecItemAdd((__bridge CFDictionaryRef)query,NULL);}
    if(status!=errSecSuccess){[self showMessage:[NSString stringWithFormat:@"保存密钥失败（Keychain %d），配置未保存",(int)status]];return;}
    [config removeObjectForKey:@"token"];
    [[NSUserDefaults standardUserDefaults] setObject:config forKey:AGTunnelDefaultsKey];
    [self dismissViewControllerAnimated:YES completion:nil];
}
@end
