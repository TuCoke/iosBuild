// ag_rec.h —— 识别栈（OCR / 目标检测 / 点阵识别）
//
// 【与参考实现的关系】
// AutoGo 把识别能力拆成 4 个独立 dylib，都是干净的 C 入口：
//   Runtime/libppocr.dylib   ppocr_new / ppocr_load / ppocr_ocr / ppocr_close
//                            ppocr_set_target_size / ppocr_set_threshold
//   Runtime/libyolo.dylib    newYolo / loadModelYolo / detectYolo / closeYolo
//   Runtime/libdotocr.dylib  dotocr_setDict / dotocr_detect / dotocr_findStr
//   Runtime/libncnn.dylib    纯 C++ 推理库（上面三个静态链了它，不用单独调）
//
// 我们不复刻它们的内部算法，而是按同样的 C ABI 去调用 —— 这是"用他的"最稳的做法：
// 行为天然一致，也不用重新训练/对齐模型。
//
// 前提：这些 dylib 里静态链了 OpenCV（接口都收 cv::Mat&），
// 所以我们也必须链同一份 OpenCV，否则 cv::Mat 的内存布局对不上会直接崩。
#import <Foundation/Foundation.h>
#import "runtime.h"

NS_ASSUME_NONNULL_BEGIN

/// 识别栈是否可用（三个 dylib 都在 + 编译期有 OpenCV 头）
BOOL AGRecognitionAvailable(void);

/// 不可用时的具体原因（给 Go 层原样返回，不假装成功）
NSString *AGRecognitionUnavailableReason(void);

/// 识别分派入口：ppocr.* / yolo.* / dotocr.*
/// 返回值与 AGJSON/AGError 同一套约定（见 runtime.h）
ag_ios_result AGRecognition(NSString *op, NSDictionary *args);

NS_ASSUME_NONNULL_END
