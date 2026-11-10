#ifndef AG_IOS_BRIDGE_H
#define AG_IOS_BRIDGE_H
#include <stdint.h>
#include <stddef.h>

// 返回内存统一由 Go 复制后调用 ag_ios_free 释放，禁止跨语言保留 Go 指针。
typedef struct {
    int code;
    uint8_t *data;
    size_t length;
    int64_t pts_us;
    int keyframe;
    int width;
    int height;
} ag_ios_result;

// 【必须包 extern "C"】
// 这个头会被 runtime.h 间接带进 .mm（ObjC++）文件。没有 C 链接的话，
// ag_ios_call / ag_ios_free 在 C++ 侧会变成修饰名，而 Go 的 cgo 按 C 符号引用，
// 链接期就互相找不到（前面 AGJSON / AGError 踩的就是这个坑）。
#ifdef __cplusplus
extern "C" {
#endif

ag_ios_result ag_ios_call(const char *method, const uint8_t *json, size_t length);
void ag_ios_free(void *data);

#ifdef __cplusplus
}
#endif

#endif
