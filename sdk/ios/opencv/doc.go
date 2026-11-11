// Package opencv 预留完整 OpenCV Go 绑定的命名空间，当前尚未提供 Mat/FindImage 等完整接口。
// 不能将预编译识别引擎使用 OpenCV 等同于这些 Go 绑定已经实现。
// 样本 images 的缩放/旋转走 Go imaging，灰度/阈值走 Go 计算；这些能力在兄弟 images 包实现。
// 识别库的 RGBA C ABI 在宿主 ag_rec.mm 中接入，与完整 Go OpenCV API 是不同边界。
package opencv
