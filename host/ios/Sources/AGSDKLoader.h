// AGSDKLoader.h —— 运行时加载 libagovios.dylib 并解析 C 入口
//
// 与参考实现一致：壳里【不链】任何 Go 产物，SDK/解释器都在 dylib 里，
// 运行时 dlopen 进来。所以改 SDK 不需要重装 App（热更新能力的基础）。
#import <Foundation/Foundation.h>

NS_ASSUME_NONNULL_BEGIN

/// dylib 的 C 入口签名（与参考实现同名同签名）
typedef char *(*AGInvokeFn)(char *method, char *payload);
typedef void (*AGFreeFn)(char *p);
typedef void (*AGGoMainFn)(void);

@interface AGSDKLoader : NSObject

/// 是否已成功加载（agov_ios_invoke / agov_ios_free 都解析到才算成功）
@property (nonatomic, assign, readonly) BOOL loaded;
/// 加载成功的 dylib 路径；失败时为 nil
@property (nonatomic, copy, readonly, nullable) NSString *path;
/// 失败原因（区分"找不到文件"和"文件在但没有入口符号"）
@property (nonatomic, copy, readonly, nullable) NSString *loadError;

+ (instancetype)shared;

/// 尝试加载。顺序：沙盒 Plugins（热更新产物）→ App 根目录 → Frameworks/
- (BOOL)loadIfNeeded;

/// 调一次 SDK。失败返回 nil，并设置 lastError。
- (nullable NSString *)invoke:(NSString *)method payload:(NSString *)payload;

/// 最近一次调用/加载的错误
@property (nonatomic, copy, readonly, nullable) NSString *lastError;

@end

NS_ASSUME_NONNULL_END
