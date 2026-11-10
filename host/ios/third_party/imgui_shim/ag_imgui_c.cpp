// ag_imgui_c.cpp —— 上面那层 C API 的实现（C++ 里直接调 ImGui::）
#include "ag_imgui_c.h"
#include "imgui.h"

void agImNewFrame(void) { ImGui::NewFrame(); }

int agImBegin(const char *name, int flags) {
    return ImGui::Begin(name, NULL, (ImGuiWindowFlags)flags) ? 1 : 0;
}

void agImEnd(void) { ImGui::End(); }

void agImText(const char *text) { ImGui::TextUnformatted(text ? text : ""); }

void agImSetNextWindowPos(float x, float y) {
    ImGui::SetNextWindowPos(ImVec2(x, y), ImGuiCond_Always);
}

void agImSetNextWindowSize(float w, float h) {
    ImGui::SetNextWindowSize(ImVec2(w, h), ImGuiCond_Always);
}

void agImPushWindowBg(unsigned int rgba) {
    // ImU32 是 0xAABBGGRR 顺序（imgui 的 IM_COL32）
    unsigned int a = (rgba >> 24) & 0xFF, b = (rgba >> 16) & 0xFF;
    unsigned int g = (rgba >> 8) & 0xFF, r = rgba & 0xFF;
    ImGui::PushStyleColor(ImGuiCol_WindowBg, IM_COL32(r, g, b, a));
}

void agImPopStyleColor(void) { ImGui::PopStyleColor(); }

void agImSetWindowFontScale(float scale) { ImGui::SetWindowFontScale(scale); }

float agImGetScrollY(void) { return ImGui::GetScrollY(); }

float agImGetScrollMaxY(void) { return ImGui::GetScrollMaxY(); }

void agImSetScrollHereY(float center) { ImGui::SetScrollHereY(center); }
