/*
 * ag_rec.h —— 识别栈的 C ABI 契约（ppocr / yolo / dotocr）
 *
 * 【为什么先定 ABI】
 * 这三个能力的实现是独立 dylib（ncnn + 模型），与主程序解耦：
 *   · Go 侧通过 dlsym 找符号，找不到就明确返回"不可用"
 *   · 实现方（我们的 libagovrec.dylib）只要满足这份头文件，就能被直接换上
 * 这样"换模型/换推理框架/换 OCR 版本"不需要重编 App，
 * 也是 AutoGo 把 libppocr/libyolo/libdotocr 做成独立 dylib 的原因。
 *
 * 【命名约定】
 * 与参考实现保持同样的前缀风格（ppocr_ / yolo_ / dotocr_），
 * 参数上补齐我们需要的：句柄化、显式尺寸、显式返回码。
 *
 * 【返回码】
 *   0  成功
 *  -1  未实现 / 库未加载
 *  -2  参数非法
 *  -3  运行失败（模型缺失、推理失败）
 */

#ifndef AG_REC_H
#define AG_REC_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* ============ PP-OCR ============ */

/* 创建一个 OCR 引擎。version: "v2" / "v5"；modelDir: 模型目录（含 det/rec/cls 与字典）。
 * 返回句柄；失败返回 NULL。 */
void *ppocr_new(const char *version, const char *modelDir);

/* 加载字典/预热。成功返回 0。 */
int ppocr_load(void *engine, const char *dictPath);

/* 在 RGB 图上识别指定区域。
 *   rgb:   行主序 RGB888，长度 w*h*3
 *   x1,y1,x2,y2: 检测区域（像素）；x2/y2 传 0 表示到边界
 *   colorStr: 文字颜色过滤，格式 "CCCCCC-101010"（"-" 后为每通道容差）
 *   outJSON: 输出 JSON 数组（调用方负责 free）
 * 返回 Result[] 的 JSON，元素字段：X/Y/宽/高/标签/精度
 */
int ppocr_ocr(void *engine, const uint8_t *rgb, int w, int h,
              int x1, int y1, int x2, int y2,
              const char *colorStr, char **outJSON);

/* 释放引擎。 */
void ppocr_close(void *engine);

/* ============ YOLO ============ */

/* version: "v5" / "v8"；cpuThreadNum: 推理线程数；
 * paramPath/binPath: ncnn 模型；labels: 标签文件。失败返回 NULL。 */
void *yolo_new(const char *version, int cpuThreadNum,
               const char *paramPath, const char *binPath, const char *labels);

/* 设置下次 detect 使用的图（RGB888）。返回 0。 */
int yolo_set_image(void *engine, const uint8_t *rgb, int w, int h);

/* 检测。outJSON 语义同 ppocr_ocr。 */
int yolo_detect(void *engine, char **outJSON);

/* 直接在给定图上检测（不改引擎内部图）。 */
int yolo_detect_image(void *engine, const uint8_t *rgb, int w, int h, char **outJSON);

void yolo_close(void *engine);

/* ============ 点阵 OCR ============ */

/* dictPath: 字符点阵字典。失败返回 NULL。 */
void *dotocr_new(const char *dictPath);

/* 识别整段文本，返回字符串（调用方 free）。 */
int dotocr_ocr(void *engine, const uint8_t *rgb, int w, int h,
               int x1, int y1, int x2, int y2, char **outText);

/* 在图中找子串，返回命中的中心坐标（写入 cx/cy）；未命中返回 -3。 */
int dotocr_find_str(void *engine, const uint8_t *rgb, int w, int h,
                    int x1, int y1, int x2, int y2,
                    const char *needle, int *cx, int *cy);

void dotocr_close(void *engine);

/* ============ 库自述（可选） ============ */

/* 返回实现版本串，例如 "ncnn-20240820 ppocr-v5"；用于 capabilities 里展示。 */
const char *ag_rec_version(void);

#ifdef __cplusplus
}
#endif

#endif /* AG_REC_H */
