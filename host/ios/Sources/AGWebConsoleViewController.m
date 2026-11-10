// AGWebConsoleViewController.m
#import "AGWebConsoleViewController.h"
#import <WebKit/WebKit.h>

@interface AGWebConsoleViewController () <WKNavigationDelegate>
@property (nonatomic, strong) WKWebView *webView;
@property (nonatomic, strong) NSURL *url;
@end

@implementation AGWebConsoleViewController

- (instancetype)initWithURL:(NSURL *)url {
    self = [super initWithNibName:nil bundle:nil];
    if (self) {
        _url = url;
    }
    return self;
}

- (void)viewDidLoad {
    [super viewDidLoad];
    self.title = @"Web 控制台";
    self.view.backgroundColor = UIColor.systemBackgroundColor;

    // 用 WKWebViewConfiguration 显式允许内联媒体与 JS：本地控制台要靠 JS 跑
    WKWebViewConfiguration *cfg = [[WKWebViewConfiguration alloc] init];
    cfg.allowsInlineMediaPlayback = YES;
    cfg.defaultWebpagePreferences.allowsContentJavaScript = YES;

    _webView = [[WKWebView alloc] initWithFrame:CGRectZero configuration:cfg];
    _webView.translatesAutoresizingMaskIntoConstraints = NO;
    _webView.navigationDelegate = self;
    [self.view addSubview:_webView];

    [NSLayoutConstraint activateConstraints:@[
        [_webView.topAnchor constraintEqualToAnchor:self.view.safeAreaLayoutGuide.topAnchor],
        [_webView.bottomAnchor constraintEqualToAnchor:self.view.bottomAnchor],
        [_webView.leadingAnchor constraintEqualToAnchor:self.view.leadingAnchor],
        [_webView.trailingAnchor constraintEqualToAnchor:self.view.trailingAnchor],
    ]];

    self.navigationItem.rightBarButtonItem =
        [[UIBarButtonItem alloc] initWithTitle:@"关闭"
                                         style:UIBarButtonItemStylePlain
                                        target:self
                                        action:@selector(close)];

    NSURLRequest *req = [NSURLRequest requestWithURL:_url
                                         cachePolicy:NSURLRequestReloadIgnoringLocalCacheData
                                     timeoutInterval:10];
    [_webView loadRequest:req];
}

- (void)close {
    [self dismissViewControllerAnimated:YES completion:nil];
}

#pragma mark - WKNavigationDelegate

- (void)webView:(WKWebView *)webView didFailProvisionalNavigation:(WKNavigation *)navigation withError:(NSError *)error {
    // 本地服务没起来时这里会失败：明确告诉用户，而不是白屏
    NSString *html = [NSString stringWithFormat:
        @"<html><body style='font:16px -apple-system;padding:24px'>"
        @"<h3>打不开本地控制台</h3><p>%@</p>"
        @"<p>先点「能力探测」附近的接口启动本地服务（localapi.start），再重试。</p>"
        @"</body></html>", error.localizedDescription];
    [webView loadHTMLString:html baseURL:nil];
}

@end
