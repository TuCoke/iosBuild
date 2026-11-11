// ag_bridge.h —— cimgui 的 C 包装（给 Go 的 cgo 用）
//
// 【为什么需要这一层】
// cimgui.h 会包含 imgui 的头（imgui.h / imgui_freetype.h），那些是 C++ 头。
// 而 Go 的 cgo 把 .go 文件里的 #include 当 C 编译 → 直接 include cimgui.h
// 会炸（实测 1639 errors）。
// 所以：头文件只放纯 C 声明（本文件），真正的 C++ 实现放到 ag_bridge.cpp，
// cgo 编 .cpp 用的是 CXX（clang++），就没问题了。
//
// 【符号仍是 ig*】
// 实现在 ag_bridge.cpp 里直接调 igBegin / igTextUnformatted 这些，
// 所以 libimgui.dylib 导出的名字与参考实现一致。
#ifndef AG_IMGUI_BRIDGE_H
#define AG_IMGUI_BRIDGE_H

#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef void (*agImDrawCallback)(void);
void agImSetDrawCallback(agImDrawCallback callback);
void agImDrawScript(void);
int agImFrameActive(void);
int agImCheckbox(const char *label,int *value);
int agImSliderFloat(const char *label,float *value,float min,float max);
int agImSliderInt(const char *label,int *value,int min,int max);
int agImInputText(const char *label,char *value,size_t capacity);
void  agImNewFrame(void);
int   agImBegin(const char *name);
void  agImEnd(void);
void  agImText(const char *text);
void  agImSetNextWindowPos(float x, float y);
void  agImSetNextWindowSize(float w, float h);
void  agImPushWindowBg(unsigned int rgba);
void  agImPopStyleColor(void);
float agImScrollY(void);
float agImScrollMaxY(void);
void  agImSetScrollHereY(float center);
float agImFontSize(void);

#ifdef __cplusplus
}
#endif

#endif
