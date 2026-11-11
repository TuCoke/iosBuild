// 识别引擎通过 Runtime 动态库调用。ABI 依据 cgo 调用点，输入为连续 RGBA，返回字符串由调用方释放。
// 引擎内部依赖 NCNN/OpenCV，不能把导出的 C 接口误声明成 cv::Mat 引用。
#import <Foundation/Foundation.h>
#import "runtime.h"

NS_ASSUME_NONNULL_BEGIN

// 【必须包 extern "C"】
// 这个头被 ag_rec.mm（ObjC++）包含并实现。没有 C 链接的话，
// AGRecognition 会被编成 C++ 修饰名，而 runtime_ios.m 按 C 符号引用它 —— 链接期找不到。
#ifdef __cplusplus
extern "C" {
#endif

/// 识别栈是否可用（至少一个识别能力的完整入口可用）
BOOL AGRecognitionAvailable(void);

/// 不可用时的具体原因（给 Go 层原样返回，不假装成功）
NSString *AGRecognitionUnavailableReason(void);

/// 识别分派入口：ppocr.* / yolo.* / dotocr.*
/// 返回值与 AGJSON/AGError 同一套约定（见 runtime.h）
ag_ios_result AGRecognition(NSString *op, NSDictionary *args);


#ifdef __cplusplus
}
#endif
NS_ASSUME_NONNULL_END
