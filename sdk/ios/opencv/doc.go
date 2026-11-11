// Package opencv 提供由原生 OpenCV 执行的 Mat、编解码、裁剪、缩放、旋转、灰度、阈值、模糊、边缘与模板匹配。
// Mat 使用显式 Close 和令牌注册表管理；原生胶水在 host/ios/objc/ag_cv.mm。
// 这是一组实际实现的图像能力，不声称已覆盖样本所有 OpenCV 类型和函数。
// 未交付 OpenCV 运行库的制品明确返回不支持，不用空实现代替。
package opencv
