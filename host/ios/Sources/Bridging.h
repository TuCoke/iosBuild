//
// Bridging.h — Swift 与 SDK 之间的桥接头。
//
// 两层来源：
//   · 静态：libagovios.h（`go build -buildmode=c-archive` 自动生成，链进主程序）
//   · 动态：libagovios.dylib（`-buildmode=c-shared`，运行时 dlopen，可热更）
//
// Swift 一律调 AGSDKInvoke —— 由 AgovSDKLoader.m 决定用哪一个，
// 上层不需要知道当前是静态还是动态（诊断界面可查 AGSDKIsDynamic）。
//
#ifndef AGOV_HOST_BRIDGING_H
#define AGOV_HOST_BRIDGING_H

#include "libagovios.h"

// 由 AgovSDKLoader.m 实现。
char *AGSDKInvoke(const char *method, const char *payload);
void AGSDKFree(char *p);
int AGSDKIsDynamic(void);
char *AGSDKLoadedPath(void);
char *AGSDKLoadError(void);
int AGSDKReload(void);

#endif
