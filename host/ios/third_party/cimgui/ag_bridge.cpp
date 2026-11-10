// ag_bridge.cpp —— cimgui 的 C++ 实现层（cgo 用 CXX 编它）
//
// 头文件 ag_bridge.h 只有纯 C 声明，这样 Go 的 #include 不会碰到 C++ 头。
// 这里 include cimgui.h（它会拉进 imgui.h / imgui_freetype.h），
// 再把 ig* 包一层 agIm*，名字保持可读、且不与 cimgui 冲突。
//
// 注意：cimgui 内部按 "./imgui/..." 引用 imgui，所以 -I 必须包含
// host/ios/third_party/cimgui（cimgui.h 所在）和 cimgui/imgui（imgui.h 所在）。
//
// 【顺序很重要】必须先 include imgui.h + imgui_internal.h，再 include cimgui.h。
// cimgui.h 里有一批内部类型（ImTextureData / ImGuiTable / ImPool<> / ImSpan<> 等），
// 它们只在 imgui_internal.h 里定义；只含 cimgui.h 会报一堆
// "unknown type name 'ImTextureData'"。cimgui.cpp 自己也是这个顺序。
#include "ag_bridge.h"
#include "imgui.h"
#include "imgui_internal.h"
#include "cimgui.h"

extern "C" {

void agImNewFrame(void) { igNewFrame(); }

int agImBegin(const char *name) {
    return igBegin(name, NULL, 0) ? 1 : 0;
}

void agImEnd(void) { igEnd(); }

void agImText(const char *text) { igTextUnformatted(text ? text : "", NULL); }

void agImSetNextWindowPos(float x, float y) {
    ImVec2_c p;
    p.x = x;
    p.y = y;
    igSetNextWindowPos(p, 0, p);
}

void agImSetNextWindowSize(float w, float h) {
    ImVec2_c s;
    s.x = w;
    s.y = h;
    igSetNextWindowSize(s, 0);
}

void agImPushWindowBg(unsigned int rgba) {
    // 【为什么不再自己做位运算】
    // 之前是把 RGBA 拼成 0xAABBGGRR 再解回 IM_COL32，中间多一层反转，容易错。
    // cimgui 直接给了 igPushStyleColor_Vec4，传 0~1 的浮点分量即可，语义无歧义。
    // 入参仍然是 RGBA 各 0-255（Go 侧传的就是这个）。
    ImVec4_c c;
    c.x = ((rgba) & 0xFF) / 255.0f;          // R
    c.y = ((rgba >> 8) & 0xFF) / 255.0f;     // G
    c.z = ((rgba >> 16) & 0xFF) / 255.0f;    // B
    c.w = ((rgba >> 24) & 0xFF) / 255.0f;    // A
    igPushStyleColor_Vec4(ImGuiCol_WindowBg, c);
}

void agImPopStyleColor(void) { igPopStyleColor(1); }

float agImScrollY(void) { return igGetScrollY(); }

float agImScrollMaxY(void) { return igGetScrollMaxY(); }

void agImSetScrollHereY(float center) { igSetScrollHereY(center); }

float agImFontSize(void) { return igGetFontSize(); }

} // extern "C"
