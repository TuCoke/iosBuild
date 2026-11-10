/*
 * ag_bridge.h —— 原生能力层（与参考实现的 _Bridge* 一一对应）
 *
 * 【为什么用这个名字】
 * 逆向确认参考实现的 76 个能力都叫 Bridge<Xxx>，由 Go 通过 cgo 调用。
 * 我们照同样的命名与职责建这一层：命名对齐，看代码时两边能一一对上。
 *
 * 【约定】
 *   返回 int 的：0 表示成功，负数是错误码
 *   返回 char* 的：由本层 malloc，调用方 free（与 AXSweepNodesJSON 一致）
 *   坐标一律用物理像素；与 uiacc/motion 的"归一化坐标"不同，注意别混
 */
#ifndef AG_BRIDGE_H
#define AG_BRIDGE_H

#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* ---- 输入 ---- */
int  BridgeTouchDownFinger(double x, double y, int finger);
int  BridgeTouchMoveFinger(double x, double y, int finger);
int  BridgeTouchUpFinger(double x, double y, int finger);
int  BridgePressHome(void);
int  BridgePressKey(int keyCode);

/* ---- 屏幕 ---- */
int  BridgeNativeSize(char *out, size_t outLen);      /* "width,height" */
double BridgeScreenScale(void);
int  BridgeScreenOrientation(void);
int  BridgeScreenBrightness(void);                    /* 0-100 */
void BridgeSetScreenBrightness(int percent);

/* ---- 应用 ---- */
int  BridgeOpenApp(const char *bundleId);
int  BridgeKillApp(const char *bundleId);
char *BridgeFrontmostApp(void);
char *BridgeSelfBundleID(void);
int  BridgeOpenURL(const char *url);
char *BridgeGetAppList(void);                         /* JSON */
char *BridgeGetAppName(const char *bundleId);
char *BridgeGetAppVersion(const char *bundleId);
char *BridgeGetAppBundlePath(const char *bundleId);
char *BridgeGetAppDataPath(const char *bundleId);
int  BridgeIsAppInstalled(const char *bundleId);
int  BridgeGetAppIcon(const char *bundleId, void **outData, size_t *outLen);
int  BridgeInstallApp(const char *path);
int  BridgeUninstallApp(const char *bundleId);
int  BridgeClearAppData(const char *bundleId);

/* ---- 系统信息 ---- */
char *BridgeDeviceModel(void);
char *BridgeSystemVersion(void);
char *BridgeSerialNumber(void);
int  BridgeBatteryLevel(void);                        /* -1 未知，否则 0-100 */
int  BridgeBatteryState(void);                        /* 0 未知 1 放电 2 充电 3 满 */
int  BridgeIsScreenOn(void);
int  BridgeIsScreenLocked(void);
long long BridgeTotalMemory(void);                    /* MB */
long long BridgeAvailableMemory(void);                /* MB */
int  BridgeKeepScreenOn(int on);
int  BridgeWakeUp(void);
int  BridgeGetPidByName(const char *name);
long long BridgeGetMemoryUsageKB(int pid);
double BridgeGetCpuUsage(int pid);
int  BridgeReboot(void);

/* ---- 网络 ---- */
int  BridgeVpnStatus(void);
int  BridgeSetVPNEnabled(int enabled);
int  BridgeSetWiFiEnabled(int enabled);

/* ---- 文本 / 弹窗 ---- */
int  BridgeShowToast(const char *text, double durationMs);
int  BridgeInputText(const char *text);
char *BridgeGetClipboard(void);
int  BridgeSetClipboard(const char *text);
int  BridgeAlert(const char *title, const char *content, const char *buttons);
int  BridgeInputAlert(const char *title, const char *content, const char *placeholder, char **outText);

/* ---- 悬浮层 ---- */
int  BridgeHudCreate(int h);
int  BridgeHudDestroy(int h);
int  BridgeHudShow(int h, int x1, int y1, int x2, int y2);
int  BridgeHudHide(int h);
int  BridgeHudIsVisible(int h);
int  BridgeHudSetPosition(int h, int x1, int y1, int x2, int y2);
int  BridgeHudSetBgColor(int h, const char *hex);
int  BridgeHudSetFontSize(int h, int size);
int  BridgeHudSetText(int h, const char *text);

int  BridgeConsoleCreate(int h);
int  BridgeConsoleDestroy(int h);
int  BridgeConsoleShow(int h, int x, int y, int w, int hgt);
int  BridgeConsoleHide(int h);
int  BridgeConsoleIsVisible(int h);
int  BridgeConsoleLog(int h, const char *text);
int  BridgeConsoleClear(int h);
int  BridgeConsoleSetPosition(int h, int x, int y, int w, int hgt);
int  BridgeConsoleSetSize(int h, int w, int hgt);
int  BridgeConsoleSetFontSize(int h, int size);
int  BridgeConsoleSetBgColor(int h, const char *hex);

/* ---- 采集 ---- */
int  BridgeH264SetCallback(void *cb);
int  BridgeH264SetEncoderParams(int width, int height, int bitRate, int fps);
int  BridgeH264StartEncoding(void);
int  BridgeH264StopEncoding(void);
int  BridgeRawFrameSetCallback(void *cb);
int  BridgeRawFrameStart(int fps);

/* ---- 进程 ---- */
int  BridgeSpawnRootBinary(const char *path, const char *args, int *outPid);

#ifdef __cplusplus
}
#endif

#endif /* AG_BRIDGE_H */
