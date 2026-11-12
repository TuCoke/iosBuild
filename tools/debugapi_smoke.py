"""模拟器真实 HTTP 冒烟；token 只放请求头，不打印，也不写测试报告。"""
import json
import re
import pathlib
import sys
import time
import urllib.request
import urllib.error


def run(documents):
    token = None
    for _ in range(40):
        log = (documents / "app.log").read_text(encoding="utf-8")
        for line in log.splitlines():
            if line.startswith("debugapi.start → "):
                reply = json.loads(line.split(" → ", 1)[1])
                if reply.get("ok"):
                    token = reply["data"]["token"]
        if token:
            break
        time.sleep(0.5)
    if not token:
        raise RuntimeError("调试服务没有返回启动成功和 token")
    if token != "1234567":
        raise RuntimeError("调试服务没有使用约定的固定 token")

    def call(path, method="GET", auth=True):
        req = urllib.request.Request("http://127.0.0.1:18990" + path, method=method)
        if auth:
            req.add_header("Authorization", "Bearer " + token)
        # 前台状态轮询可能与请求短暂竞争；409 表示动作尚未开始，可有界重试。
        for attempt in range(10):
            try:
                with urllib.request.urlopen(req, timeout=125) as response:
                    return response.read()
            except urllib.error.HTTPError as exc:
                if exc.code != 409 or attempt == 9:
                    raise
                time.sleep(0.2)

    try:
        call("/status", auth=False)
    except urllib.error.HTTPError as exc:
        if exc.code != 401:
            raise
    else:
        raise RuntimeError("未授权请求被错误放行")
    results = {"unauthenticatedStatus": 401}
    for path in ["/health", "/status", "/api", "/capabilities", "/worker", "/selftest", "/diagnostics/worker", "/screenshot", "/ui-tree", "/video/status"]:
        data = json.loads(call(path))
        if not data.get("ok"):
            raise RuntimeError("调试调用失败: " + path)
        results[path] = {"ok": True}
    data = json.loads(call("/script/test", method="POST"))
    if not data.get("ok") or not data.get("data", {}).get("ok"):
        raise RuntimeError("调试内置脚本失败")
    results["/script/test"] = {"ok": True}
    if not call("/log"):
        raise RuntimeError("调试日志为空")
    # 真正尝试短时采集；模拟器硬件编码/系统采集限制单独登记，不能冒充真机通过。
    video_request = urllib.request.Request("http://127.0.0.1:18990/video/stream?durationSeconds=2&maxSize=256&fps=5&bitrate=200000")
    video_request.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(video_request, timeout=15) as response:
            sample = response.read((8 << 20) + 1)
        if len(sample) > 8 << 20:
            raise RuntimeError("模拟器视频样本超过上限")
        nal_types = sorted({packet[0] & 31 for packet in re.split(b"\x00\x00(?:\x00)?\x01", sample) if packet})
        valid = all(kind in nal_types for kind in (5, 7, 8))
        results["videoCaptureProbe"] = {"ok": valid, "bytes": len(sample), "nalTypes": nal_types, "deviceValidated": False}
        (documents / "debug-video.h264").write_bytes(sample)
    except Exception as exc:
        results["videoCaptureProbe"] = {"ok": False, "error": str(exc), "deviceValidated": False}
    results["videoAfterProbe"] = json.loads(call("/video/status"))
    report = documents / "debugapi-smoke.json"
    report.write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8")
    print("调试 HTTP 鉴权与设备/脚本入口检查通过；视频实际探测结果见 videoCaptureProbe，未输出 token")


if __name__ == "__main__":
    run(pathlib.Path(sys.argv[1]))
