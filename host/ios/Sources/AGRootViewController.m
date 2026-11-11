// AGRootViewController.m —— 主界面 + 启动自检 + 日志落盘
//
// 【界面为什么是这样的】
// 和参考实现的验证套路一致：一个能被"注入点击"命中的目标块 + 一排能力按钮。
// 目标块必须挂手势，否则"被注入点击：0"永远不变，就无法证明注入生效。
//
// 【日志为什么要落盘】
// CI 只能拿文件；界面上的 UITextView 文字取不到。
// 所以每行同时写 Documents/app.log，CI 用 simctl get_app_container 取出来当 artifact。
#import "AGRootViewController.h"
#import "AGBridge.h"
#import "AGWebConsoleViewController.h"

@interface AGRootViewController () <UITextViewDelegate>
@property (nonatomic, strong) UITextView *logView;
@property (nonatomic, strong) UILabel *counterLabel;
@property (nonatomic, strong) UIImageView *shotView;
@property (atomic, assign) NSInteger tapCount;
/// 目标块中心（归一化到整屏），注入点击瞄准这里
@property (nonatomic, assign) CGPoint targetCenter;
@property (nonatomic, assign) BOOL didStartup;
@property (nonatomic, strong) dispatch_queue_t sdkQueue;
@end

@implementation AGRootViewController

#pragma mark - 生命周期

- (void)viewDidLoad {
    [super viewDidLoad];
    self.title = @"AgovHost";
    self.sdkQueue=dispatch_queue_create("agov.host.sdk.actions",DISPATCH_QUEUE_SERIAL);
    self.view.backgroundColor = UIColor.systemBackgroundColor;
    self.targetCenter = CGPointMake(0.5, 0.35);
    [self buildUI];
}

- (void)viewDidAppear:(BOOL)animated {
    [super viewDidAppear:animated];
    // viewDidAppear 也会被多次调用（返回 Web 控制台时会再来一次），
    // 不加闸门脚本会跑好几遍、日志重复。
    if (self.didStartup) {
        return;
    }
    self.didStartup = YES;
    [self resetLogFile];
    [self append:@"AgovHost 启动（ObjC 壳）"];
    // SDK 调用不能堵住 UIKit 主线程，解释器和原生采集都可能需要回到主线程。
    dispatch_async(self.sdkQueue, ^{
    [self callCapabilities];
    [self callSelfTest];
    [self runScript];
    [self append:[AGBridge statusLine]];
    [self probeAllButtons];
    [self runAllAPITest];
    [self append:[NSString stringWithFormat:@"localapi.start → %@",
                  [AGBridge invoke:@"localapi.start" payload:@"{}"]]];
    });
}

#pragma mark - 界面

- (void)buildUI {
    UIStackView *root = [[UIStackView alloc] init];
    root.axis = UILayoutConstraintAxisVertical;
    root.spacing = 8;
    root.translatesAutoresizingMaskIntoConstraints = NO;
    [self.view addSubview:root];

    UILayoutGuide *safe = self.view.safeAreaLayoutGuide;
    [NSLayoutConstraint activateConstraints:@[
        [root.topAnchor constraintEqualToAnchor:safe.topAnchor constant:8],
        [root.leadingAnchor constraintEqualToAnchor:safe.leadingAnchor constant:12],
        [root.trailingAnchor constraintEqualToAnchor:safe.trailingAnchor constant:-12],
        [root.bottomAnchor constraintEqualToAnchor:safe.bottomAnchor constant:-8],
    ]];

    // 4 个占位行：用来验证"滑动注入"有没有到
    UIStackView *rows = [[UIStackView alloc] init];
    rows.axis = UILayoutConstraintAxisVertical;
    rows.spacing = 6;
    rows.distribution = UIStackViewDistributionFillEqually;
    for (NSInteger i = 1; i <= 4; i++) {
        UILabel *row = [[UILabel alloc] init];
        row.text = [NSString stringWithFormat:@"  列表行 %ld — 用来验证滑动注入", (long)i];
        row.backgroundColor = [UIColor.systemGray5Color colorWithAlphaComponent:0.6];
        row.layer.cornerRadius = 6;
        row.clipsToBounds = YES;
        row.font = [UIFont systemFontOfSize:16 weight:UIFontWeightMedium];
        [rows addArrangedSubview:row];
    }
    [root addArrangedSubview:rows];
    [rows.heightAnchor constraintEqualToConstant:150].active = YES;

    // 中央目标块：注入点击命中时计数 +1（必须挂手势，否则永远证明不了）
    _counterLabel = [[UILabel alloc] init];
    _counterLabel.text = @"被注入点击：0";
    _counterLabel.font = [UIFont boldSystemFontOfSize:20];
    _counterLabel.textColor = UIColor.whiteColor;
    _counterLabel.textAlignment = NSTextAlignmentCenter;
    _counterLabel.backgroundColor = UIColor.systemRedColor;
    _counterLabel.layer.cornerRadius = 10;
    _counterLabel.clipsToBounds = YES;
    _counterLabel.userInteractionEnabled = YES;
    [_counterLabel addGestureRecognizer:
        [[UITapGestureRecognizer alloc] initWithTarget:self action:@selector(onTargetTapped)]];
    [root addArrangedSubview:_counterLabel];
    [_counterLabel.heightAnchor constraintEqualToConstant:84].active = YES;

    // 三排按钮（与 Swift 版一致，共 12 个）
    [root addArrangedSubview:[self buttonRow:@[
        @[@"能力探测", @"callCapabilities"],
        @[@"自检", @"callSelfTest"],
        @[@"设备信息", @"callDeviceInfo"],
        @[@"截图", @"callScreenshot"],
    ]]];
    [root addArrangedSubview:[self buttonRow:@[
        @[@"Web 控制台", @"openWebConsole"],
        @[@"启动穿透", @"tunnelStart"],
        @[@"停止穿透", @"tunnelStop"],
        @[@"隧道状态", @"tunnelStatus"],
    ]]];
    [root addArrangedSubview:[self buttonRow:@[
        @[@"跑脚本", @"runScript"],
        @[@"注入点击(中心)", @"injectTap"],
        @[@"注入上滑", @"injectSwipe"],
        @[@"清空日志", @"clearLog"],
    ]]];

    // 截图预览（成功时有图，失败时隐藏）
    _shotView = [[UIImageView alloc] init];
    _shotView.contentMode = UIViewContentModeScaleAspectFit;
    _shotView.hidden = YES;
    [root addArrangedSubview:_shotView];
    [_shotView.heightAnchor constraintEqualToConstant:80].active = YES;

    // 日志区
    _logView = [[UITextView alloc] init];
    _logView.editable = NO;
    _logView.font = [UIFont monospacedSystemFontOfSize:11 weight:UIFontWeightRegular];
    _logView.backgroundColor = UIColor.secondarySystemBackgroundColor;
    _logView.layer.cornerRadius = 8;
    _logView.textContainerInset = UIEdgeInsetsMake(6, 6, 6, 6);
    [root addArrangedSubview:_logView];
}

- (UIStackView *)buttonRow:(NSArray<NSArray<NSString *> *> *)items {
    UIStackView *row = [[UIStackView alloc] init];
    row.axis = UILayoutConstraintAxisHorizontal;
    row.spacing = 6;
    row.distribution = UIStackViewDistributionFillEqually;
    for (NSArray<NSString *> *pair in items) {
        UIButton *b = [UIButton buttonWithType:UIButtonTypeSystem];
        [b setTitle:pair.firstObject forState:UIControlStateNormal];
        b.titleLabel.font = [UIFont systemFontOfSize:14];
        b.titleLabel.adjustsFontSizeToFitWidth = YES;
        SEL sel = NSSelectorFromString(pair.lastObject);
        __weak typeof(self) weakSelf=self;__weak UIButton *weakButton=b;
        [b addAction:[UIAction actionWithHandler:^(UIAction *action){
            typeof(self) controller=weakSelf;UIButton *button=weakButton;if(!controller || !button)return;
            if([pair.lastObject isEqualToString:@"openWebConsole"]){[controller openWebConsole];return;}
            button.enabled=NO;
            dispatch_async(controller.sdkQueue,^{
                @try{NSMethodSignature *signature=[controller methodSignatureForSelector:sel];if(signature){NSInvocation *invocation=[NSInvocation invocationWithMethodSignature:signature];invocation.target=controller;invocation.selector=sel;[invocation invoke];}}
                @catch(NSException *exception){[controller append:exception.reason ?: @"SDK 调用异常"];}
                dispatch_async(dispatch_get_main_queue(),^{button.enabled=YES;});
            });
        }] forControlEvents:UIControlEventTouchUpInside];
        [row addArrangedSubview:b];
    }
    [row.heightAnchor constraintEqualToConstant:34].active = YES;
    return row;
}

#pragma mark - 目标块与注入

- (void)onTargetTapped {
    self.tapCount += 1;
    self.counterLabel.text = [NSString stringWithFormat:@"被注入点击：%ld", (long)self.tapCount];
    [self append:[NSString stringWithFormat:@"（界面）目标块被点击，计数 = %ld", (long)self.tapCount]];
}

/// 目标块中心换算成"归一化到整屏"的坐标：注入接口要的就是这个。
- (CGPoint)currentTargetCenter {
    if(!NSThread.isMainThread){__block CGPoint result;dispatch_sync(dispatch_get_main_queue(),^{result=[self currentTargetCenter];});return result;}
    if (self.counterLabel.window == nil) {
        return self.targetCenter;
    }
    CGRect frame = [self.counterLabel.superview convertRect:self.counterLabel.frame toView:nil];
    CGSize screen = UIScreen.mainScreen.bounds.size;
    if (screen.width <= 0 || screen.height <= 0) {
        return self.targetCenter;
    }
    // 注入接口要的是【归一化】坐标（0..1），不是像素。
    // 之前这里直接返回了像素中心（如 600,400），motion.tap 判成
    // "手指编号或坐标无效" —— 日志里"注入点击(目标块) → ok:false"就是它。
    CGFloat nx = (frame.origin.x + frame.size.width / 2.0) / screen.width;
    CGFloat ny = (frame.origin.y + frame.size.height / 2.0) / screen.height;
    // 夹到 (0.01, 0.99)：0/1 这种边界值有的实现会判越界
    if (isnan(nx) || isinf(nx)) { nx = 0.5; }
    if (isnan(ny) || isinf(ny)) { ny = 0.5; }
    nx = MAX(0.01, MIN(0.99, nx));
    ny = MAX(0.01, MIN(0.99, ny));
    return CGPointMake(nx, ny);
}

#pragma mark - 按钮动作

- (void)callCapabilities {
    [self append:[NSString stringWithFormat:@"capabilities → %@", [AGBridge invoke:@"capabilities" payload:@"{}"]]];
}

- (void)callSelfTest {
    [self append:[NSString stringWithFormat:@"runtime.selftest → %@",
                  [AGBridge invoke:@"runtime.selftest" payload:@"{\"x\":0.5,\"y\":0.5}"]]];
}

- (void)callDeviceInfo {
    [self append:[NSString stringWithFormat:@"device.info → %@", [AGBridge invoke:@"device.info" payload:@"{}"]]];
}

- (void)callScreenshot {
    NSString *r = [AGBridge invoke:@"capture.screenshot"
                           payload:@"{\"maxSize\":1024,\"jpeg\":true,\"quality\":0.8}"];
    [self append:[NSString stringWithFormat:@"截图 → %@", [self shorten:r to:160]]];

    // 解析 base64 落到 Documents：CI 取沙盒文件即可拿到图，不必从日志里抠
    NSData *data = [self dataFromBase64InJSON:r];
    if (data.length > 0) {
        UIImage *img = [UIImage imageWithData:data];
        dispatch_async(dispatch_get_main_queue(),^{self.shotView.image=img;self.shotView.hidden=(img==nil);});
        NSString *path = [[self documentsDir] stringByAppendingPathComponent:@"capture.jpg"];
        [data writeToFile:path atomically:YES];
        [self append:[NSString stringWithFormat:@"截图已保存 %lu 字节 → %@",
                      (unsigned long)data.length, path]];
    }
}

- (void)openWebConsole {
    NSURL *url = [NSURL URLWithString:@"http://127.0.0.1:18989/ui/index.html"];
    AGWebConsoleViewController *vc = [[AGWebConsoleViewController alloc] initWithURL:url];
    UINavigationController *nav = [[UINavigationController alloc] initWithRootViewController:vc];
    [self presentViewController:nav animated:YES completion:nil];
}

- (void)tunnelStart {
    [self append:[NSString stringWithFormat:@"启动穿透 → %@",
                  [AGBridge invoke:@"tunnel.start" payload:@"{}"]]];
}

- (void)tunnelStop {
    [self append:[NSString stringWithFormat:@"停止穿透 → %@",
                  [AGBridge invoke:@"tunnel.stop" payload:@"{}"]]];
}

- (void)tunnelStatus {
    [self append:[NSString stringWithFormat:@"隧道状态 → %@",
                  [AGBridge invoke:@"tunnel.status" payload:@"{}"]]];
}

- (void)runScript {
    [self append:[NSString stringWithFormat:@"script.run → %@",
                  [self shorten:[AGBridge invoke:@"script.run"
                                         payload:@"{\"name\":\"demo\",\"x\":0.5,\"y\":0.5}"]
                             to:400]]];
}

- (void)injectTap {
    CGPoint p = [self currentTargetCenter];
    NSInteger before = self.tapCount;
    NSString *payload = [NSString stringWithFormat:@"{\"x\":%.4f,\"y\":%.4f}", p.x, p.y];
    NSString *r = [AGBridge invoke:@"motion.tap" payload:payload];
    [self append:[NSString stringWithFormat:@"注入点击(目标块) → %@", [self shorten:r to:120]]];

    // 给系统一点时间把事件派发到手势，再对比计数：这才是"注入是否生效"的证据
    __weak typeof(self) weakSelf = self;
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.8 * NSEC_PER_SEC)),
                   dispatch_get_main_queue(), ^{
        typeof(self) self2 = weakSelf;
        if (self2 == nil) {
            return;
        }
        NSInteger after = self2.tapCount;
        NSString *verdict = after > before ? @"生效" : @"未生效（模拟器常见）";
        [self2 append:[NSString stringWithFormat:@"注入效果：计数 %ld → %ld（%@）",
                       (long)before, (long)after, verdict]];
    });
}

- (void)injectSwipe {
    [self append:[NSString stringWithFormat:@"注入上滑 → %@",
                  [AGBridge invoke:@"motion.swipe"
                           payload:@"{\"x1\":0.5,\"y1\":0.75,\"x2\":0.5,\"y2\":0.35,\"durationMs\":300}"]]];
}

- (void)clearLog {
    [self.logView setText:@""];
}

#pragma mark - 启动探测

/// 把每个按钮背后的 op 各调一次：一张截图/一份日志就能看出全部状态。
- (void)probeAllButtons {
    [self append:@"—— 按钮探测开始 ——"];
    NSArray<NSArray<NSString *> *> *probes = @[
        @[@"设备信息", @"device.info", @"{}"],
        @[@"截图", @"capture.screenshot", @"{\"maxSize\":512,\"jpeg\":true,\"quality\":0.6}"],
        @[@"隧道状态", @"tunnel.status", @"{}"],
        @[@"启动穿透", @"tunnel.start", @"{}"],
        @[@"注入点击(目标块)", @"motion.tap", [self injectPayload]],
        @[@"注入上滑", @"motion.swipe", @"{\"x1\":0.5,\"y1\":0.75,\"x2\":0.5,\"y2\":0.35,\"durationMs\":300}"],
        @[@"SDK 状态", @"sdk.status", @"{}"],
    ];
    for (NSArray<NSString *> *p in probes) {
        NSString *label = p[0], *method = p[1], *payload = p[2];
        NSInteger before = self.tapCount;
        NSString *r = [AGBridge invoke:method payload:payload];
        [self append:[NSString stringWithFormat:@"%@ → %@", label, [self shorten:r to:200]]];
        if ([method isEqualToString:@"motion.tap"]) {
            __weak typeof(self) weakSelf = self;
            dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.8 * NSEC_PER_SEC)),
                           dispatch_get_main_queue(), ^{
                typeof(self) s = weakSelf;
                if (s == nil) {
                    return;
                }
                NSString *verdict = s.tapCount > before ? @"生效" : @"未生效（模拟器常见）";
                [s append:[NSString stringWithFormat:@"注入效果：计数 %ld → %ld（%@）",
                           (long)before, (long)s.tapCount, verdict]];
            });
        }
    }
    [self append:@"—— 按钮探测结束 ——"];
}

- (NSString *)injectPayload {
    CGPoint p = [self currentTargetCenter];
    return [NSString stringWithFormat:@"{\"x\":%.4f,\"y\":%.4f}", p.x, p.y];
}

/// 全量 API 自检：逐个 op 真调一次，输出 可用/受环境限制/未实现/未验证
- (void)runAllAPITest {
    [self append:@"—— 全量 API 自检开始 ——"];
    NSString *raw = [AGBridge invoke:@"runtime.selftest.all" payload:@"{}"];
    NSData *data = [raw dataUsingEncoding:NSUTF8StringEncoding];
    NSDictionary *obj = data ? [NSJSONSerialization JSONObjectWithData:data options:0 error:NULL] : nil;
    NSDictionary *payload = [obj isKindOfClass:NSDictionary.class] ? obj[@"data"] : nil;
    if (![payload isKindOfClass:NSDictionary.class]) {
        [self append:[NSString stringWithFormat:@"全量自检调用失败：%@", [self shorten:raw to:200]]];
        [self append:@"—— 全量 API 自检结束 ——"];
        return;
    }
    [self append:[NSString stringWithFormat:@"汇总：%@", payload[@"summary"] ?: @"?"]];

    NSMutableArray<NSString *> *missing = [NSMutableArray array];
    NSMutableArray<NSString *> *limited = [NSMutableArray array];
    id rawItems = payload[@"items"];
    NSArray *items = [rawItems isKindOfClass:NSArray.class] ? rawItems : @[];
    for (NSDictionary *item in items) {
        if (![item isKindOfClass:NSDictionary.class]) {
            continue;
        }
        NSString *status = item[@"status"];
        if ([status isEqualToString:@"missing"]) {
            [missing addObject:item[@"op"] ?: @"?"];
        } else if ([status isEqualToString:@"env"]) {
            [limited addObject:[NSString stringWithFormat:@"%@：%@", item[@"op"] ?: @"?", item[@"detail"] ?: @""]];
        }
    }
    NSString *missingLine = missing.count == 0
        ? @"未实现：无"
        : [@"未实现：" stringByAppendingString:[missing componentsJoinedByString:@", "]];
    [self append:missingLine];
    [self append:[NSString stringWithFormat:@"受环境限制：%lu 项", (unsigned long)limited.count]];
    for (NSUInteger i = 0; i < MIN(limited.count, (NSUInteger)8); i++) {
        [self append:[NSString stringWithFormat:@"  %@", [self shorten:limited[i] to:80]]];
    }
    [self append:@"—— 全量 API 自检结束 ——"];
}

#pragma mark - 日志

- (void)append:(NSString *)line {
    NSString *text = line ?: @"";
    dispatch_async(dispatch_get_main_queue(), ^{
        self.logView.text = [self.logView.text stringByAppendingFormat:@"%@\n", text];
        NSRange end = NSMakeRange(self.logView.text.length, 0);
        [self.logView scrollRangeToVisible:end];
    });
    [self appendToFile:text];
}

- (NSString *)documentsDir {
    return NSSearchPathForDirectoriesInDomains(NSDocumentDirectory, NSUserDomainMask, YES).firstObject ?: NSTemporaryDirectory();
}

- (void)resetLogFile {
    NSString *path = [[self documentsDir] stringByAppendingPathComponent:@"app.log"];
    [[NSFileManager defaultManager] removeItemAtPath:path error:NULL];
}

- (void)appendToFile:(NSString *)line {
 @synchronized(self){
    NSString *path = [[self documentsDir] stringByAppendingPathComponent:@"app.log"];
    NSString *text = [line stringByAppendingString:@"\n"];
    NSData *data = [text dataUsingEncoding:NSUTF8StringEncoding];
    NSFileManager *fm = [NSFileManager defaultManager];
    if (![fm fileExistsAtPath:path]) {
        [data writeToFile:path atomically:YES];
        return;
    }
    NSFileHandle *fh = [NSFileHandle fileHandleForWritingAtPath:path];
    if (fh == nil) {
        return;
    }
    @try {
        [fh seekToEndOfFile];
        [fh writeData:data];
    } @catch (NSException *e) {
        // 写日志失败不能影响主流程
    } @finally {
        [fh closeFile];
    }
 }
}

- (NSString *)shorten:(NSString *)s to:(NSUInteger)max {
    if (s.length <= max) {
        return s;
    }
    return [[s substringToIndex:max] stringByAppendingString:@"…"];
}

/// 从 {"ok":true,"data":{"base64":"..."}} 里取图片数据
- (NSData *)dataFromBase64InJSON:(NSString *)json {
    NSData *d = [json dataUsingEncoding:NSUTF8StringEncoding];
    if (d == nil) {
        return nil;
    }
    NSDictionary *obj = [NSJSONSerialization JSONObjectWithData:d options:0 error:NULL];
    NSDictionary *data = [obj isKindOfClass:NSDictionary.class] ? obj[@"data"] : nil;
    NSString *b64 = [data isKindOfClass:NSDictionary.class] ? data[@"base64"] : nil;
    if (![b64 isKindOfClass:NSString.class] || b64.length == 0) {
        return nil;
    }
    return [[NSData alloc] initWithBase64EncodedString:b64 options:NSDataBase64DecodingIgnoreUnknownCharacters];
}

@end
