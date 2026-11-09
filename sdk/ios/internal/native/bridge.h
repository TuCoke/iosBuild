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
ag_ios_result ag_ios_call(const char *method, const uint8_t *json, size_t length);
void ag_ios_free(void *data);
#endif
