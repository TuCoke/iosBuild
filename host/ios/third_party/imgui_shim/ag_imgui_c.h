// ag_imgui_c.h —— 我们自己的 cimgui 等价层（只暴露需要的十几个函数）
//
// 【为什么不用官方 cimgui】
// 官方 cimgui 的 master 对应 imgui 1.92+，里面的 ImVector_ImWchar 等类型
// 在我们 vendor 的 imgui 1.91.5 里不存在 → 编译直接炸 1745 个错误。
// 与其去对齐版本，不如按"AutoGo 的形态"自己做一层薄 C API：
//   Go → cgo → 这层 C 函数 → ImGui::
// 形态与参考实现一致（它也是 C API + Go 绑定），但没有版本耦合。
#ifndef AG_IMGUI_C_H
#define AG_IMGUI_C_H

#ifdef __cplusplus
extern "C" {
#endif

typedef struct { float x, y; } AgImVec2;

void agImNewFrame(void);
int  agImBegin(const char *name, int flags);
void agImEnd(void);
void agImText(const char *text);
void agImSetNextWindowPos(float x, float y);
void agImSetNextWindowSize(float w, float h);
void agImPushWindowBg(unsigned int rgba);
void agImPopStyleColor(void);
void agImSetWindowFontScale(float scale);
float agImGetScrollY(void);
float agImGetScrollMaxY(void);
void agImSetScrollHereY(float center);

#ifdef __cplusplus
}
#endif

#endif
