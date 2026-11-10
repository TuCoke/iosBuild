// Package opencv 是 OpenCV（core / imgproc / imgcodecs）的 Go 绑定占位。
//
// 【为什么是占位】AutoGo 的 iOS opencv 包有 362 个导出符号（Mat 的方法 + 各模块函数），
// 同样是机械生成的绑定，不是手写逻辑。我们要复刻的是"同一套生成流程"：
//  1. 原生：opencv 三模块编成 iOS arm64 dylib
//     （AutoGo：libopencv_core 2.2 MB / imgproc 2.8 MB / imgcodecs 1.3 MB）
//  2. 绑定：从 opencv2/core.hpp、imgproc.hpp、imgcodecs.hpp 机械转换为 Go
//  3. 生成物放 opencv/gen_*.go
//
// 【现在为什么可以不生成】
// 我们的 sdk/images 已经用纯 Go 实现了业务真正会用到的那批原语：
// 灰度、二值化、自适应阈值、找色、多点找色、裁剪、缩放、旋转、灰度模板匹配。
// 它们没有 cgo、没有 25 MB 的 dylib 依赖，且在真机上跑过。
// 只有在需要"模板匹配带尺度/旋转""形态学""轮廓"这类重算法时，才值得引入 opencv。
package opencv
