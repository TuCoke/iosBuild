"""模拟器真实 HTTP 冒烟；token 只放请求头，不打印，也不写测试报告。"""
import json
import hashlib
import os
import signal
import tempfile
import re
import subprocess
import pathlib
import sys
import time
import urllib.request
import urllib.error


def input_lease_probe(documents, call):
    """真实模拟器进程：互斥、强杀、触点恢复；不把提交释放事件当成视觉确认。"""
    bundle = pathlib.Path(subprocess.run(
        ["xcrun", "simctl", "get_app_container", "booted", "dev.autogov.host", "app"],
        check=True, capture_output=True, text=True, timeout=15).stdout.strip())
    sdk = bundle / "libagovios.dylib"
    # 与宿主一致：使用已准备的版本化执行目录。安装器可能收回包内资源的执行位，
    # 不能绕过 prepareWorkerBinary，直接执行只读包内 Runtime/app。
    source_digest = hashlib.sha256((bundle / "Runtime/app").read_bytes()).digest()
    candidates = [path for path in (documents / "Worker").glob("*/app")
                  if path.is_file() and os.access(path, os.X_OK)
                  and hashlib.sha256(path.read_bytes()).digest() == source_digest]
    if len(candidates) != 1:
        raise RuntimeError("没有找到唯一且内容一致的已准备工作进程: " + str(candidates))
    binary = candidates[0]
    report = {"deviceValidated": False, "effectVerified": False}
    with tempfile.TemporaryDirectory(prefix="input-lease-ci-", dir=documents) as directory:
        directory = pathlib.Path(directory)
        params = directory / "params.json"
        ready_path = directory / "ready.json"
        params.write_text(json.dumps({"ready": str(ready_path)}), encoding="utf-8")
        # Yaegi 按包加载同目录 Go 文件，每个 main 必须放在独立工程目录，避免入口互相覆盖。
        (directory / "hold").mkdir()
        (directory / "quick").mkdir()
        hold = directory / "hold" / "main.go"
        hold.write_text('package main; import("os";"encoding/json";"autogov1/v2/sdk/ios/script/api");func main(){_,err:=api.Call("motion.pointer",map[string]any{"x":30,"y":30,"id":0,"phase":"down"});message:="";if err!=nil{message=err.Error()};b,_:=json.Marshal(map[string]any{"pid":os.Getpid(),"touchError":message});os.WriteFile(api.ParamString("ready"),b,0600);api.Sleep(30000)}', encoding="utf-8")
        quick = directory / "quick" / "main.go"
        quick.write_text('package main;import "autogov1/v2/sdk/ios/script/api";func main(){api.SetResult("lease-recovered")}', encoding="utf-8")

        def command(source, result):
            return ["xcrun", "simctl", "spawn", "booted", str(binary), "--sdk", str(sdk),
                    "--bundle", str(bundle), "--documents", str(documents), "--params", str(params),
                    "--result", str(result), "--timeout-ms", "40000", str(source)]

        owner = None
        with (directory / "hold.log").open("wb") as log:
            process = subprocess.Popen(command(hold, directory / "hold-result.json"), stdout=log, stderr=log)
            try:
                deadline = time.monotonic() + 15
                journal = documents / ".agov-device-touch.json"
                while time.monotonic() < deadline:
                    if ready_path.exists():
                        try:
                            state = json.loads(ready_path.read_text(encoding="utf-8"))
                        except json.JSONDecodeError:
                            time.sleep(0.05); continue
                        candidate = state.get("pid", 0)
                        if candidate > 0:
                            cmd = subprocess.run(["ps", "-ww", "-p", str(candidate), "-o", "command="], capture_output=True, text=True, timeout=5).stdout
                            if str(binary) in cmd and str(directory) in cmd:
                                owner = candidate; break
                    if process.poll() is not None:
                        raise RuntimeError("持有任务提前退出: " + (directory / "hold.log").read_text(errors="replace")[-3000:])
                    time.sleep(0.1)
                if owner is None:
                    raise RuntimeError("设备任务没有进入持有租约的执行阶段")
                touch_error = state.get("touchError", "")
                had_touch = not touch_error
                if touch_error and not any(word in touch_error for word in ("HID", "触摸接口不可用", "需检查授权", "事件服务接口不可用")):
                    raise RuntimeError("触摸探测发生非环境错误: " + touch_error)
                if had_touch and not json.loads(journal.read_text()).get("active"):
                    raise RuntimeError("触摸提交成功却没有记录活跃触点")
                other_result = directory / "busy-result.json"
                subprocess.run(command(quick, other_result), capture_output=True, timeout=45)
                busy = json.loads(other_result.read_text())
                if busy.get("ok") or "其他进程" not in busy.get("error", ""):
                    raise RuntimeError("另一个真实进程没有被设备租约阻止: " + json.dumps(busy, ensure_ascii=False))
                health = json.loads(call("/sdk/health"))
                if not health.get("data", {}).get("ready"):
                    raise RuntimeError("设备任务期间健康检查失败: " + json.dumps(health, ensure_ascii=False))
                # PID 来自自己的唯一任务目录，并再次核对命令；只终止本测试创建的进程。
                cmd = subprocess.run(["ps", "-ww", "-p", str(owner), "-o", "command="], capture_output=True, text=True, timeout=5).stdout
                if str(binary) not in cmd or str(directory) not in cmd:
                    raise RuntimeError("终止前进程身份发生变化")
                os.kill(owner, signal.SIGKILL); process.wait(timeout=10)
                result = directory / "recovered-result.json"
                try:
                    completed = subprocess.run(command(quick, result), capture_output=True, timeout=45)
                except subprocess.TimeoutExpired as exc:
                    output = ((exc.stdout or b"") + (exc.stderr or b"")).decode("utf-8", errors="replace")
                    detail = result.read_text(errors="replace") if result.exists() else "尚无结果文件"
                    raise RuntimeError("恢复进程超过硬期限：" + detail + "\n" + output[-6000:]) from exc
                recovered = json.loads(result.read_text())
                if completed.returncode != 0 or not recovered.get("ok"):
                    raise RuntimeError("进程退出后未恢复设备租约: " + json.dumps(recovered, ensure_ascii=False))
                if journal.exists() and json.loads(journal.read_text()).get("active"):
                    raise RuntimeError("恢复后仍记录遗留触点")
                report.update(ok=True, contentionRejected=True, healthDuringTask=True,
                              killedWorkerPID=owner, orphanReleaseSubmitted=had_touch, touchProbeSkipped=touch_error or None, leaseReacquired=True)
            finally:
                if process.poll() is None:
                    if owner:
                        cmd = subprocess.run(["ps", "-ww", "-p", str(owner), "-o", "command="], capture_output=True, text=True, timeout=5).stdout
                        if str(binary) in cmd and str(directory) in cmd:
                            os.kill(owner, signal.SIGKILL)
                    process.wait(timeout=45)
    return report


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

    def call(path, method="GET", auth=True, payload=None):
        body = None if payload is None else json.dumps(payload).encode("utf-8")
        req = urllib.request.Request("http://127.0.0.1:18990" + path, method=method, data=body)
        if body is not None:
            req.add_header("Content-Type", "application/json")
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
    for path in ["/health", "/status", "/api", "/capabilities", "/worker", "/selftest", "/diagnostics/worker", "/screenshot", "/ui-tree", "/video/status", "/background"]:
        data = json.loads(call(path))
        if not data.get("ok"):
            raise RuntimeError("调试调用失败: " + path + " " + json.dumps(data, ensure_ascii=False))
        results[path] = {"ok": True}
    data = json.loads(call("/script/test", method="POST"))
    if not data.get("ok") or not data.get("data", {}).get("ok"):
        raise RuntimeError("调试内置脚本失败")
    results["/script/test"] = {"ok": True}
    try:
        results["inputLeaseProbe"] = input_lease_probe(documents, call)
    except Exception as exc:
        results["inputLeaseProbe"] = {"ok": False, "error": str(exc), "deviceValidated": False}
        (documents / "debugapi-smoke.json").write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8")
        raise

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
    background = json.loads(call("/background"))["data"]
    if not background.get("available") or not {"debug-api", "local-api"}.issubset(background.get("owners", [])):
        raise RuntimeError("宿主后台管理器或服务运行需求未正确装配")
    if {"script-worker", "debug-video"}.intersection(background.get("owners", [])):
        raise RuntimeError("脚本/视频测试结束后仍有未清理的运行需求")
    results["backgroundBeforeSwitch"] = background
    # 模拟器短时切 App 探测，不把三秒响应当作真机长稳/锁屏验收。
    try:
        subprocess.run(["xcrun", "simctl", "launch", "booted", "com.apple.Preferences"], check=True, capture_output=True, timeout=45)
        time.sleep(3)
        results["backgroundAfterSwitch"] = json.loads(call("/background"))
        results["backgroundHealthAfterSwitch"] = json.loads(call("/health"))
    except Exception as exc:
        results["backgroundSwitchProbe"] = {"ok": False, "error": str(exc), "deviceValidated": False}
    finally:
        subprocess.run(["xcrun", "simctl", "launch", "booted", "dev.autogov.host"], capture_output=True, timeout=45)
    # 此前只在视频结束后切 App，无法捕获“接口正常但编码已停”的回归。
    # 有实际编码能力才做强断言；不支持编码的模拟器明确记为跳过。
    switch_error = None
    if results["videoCaptureProbe"].get("ok"):
        owned = False
        samples = []
        try:
            reply = json.loads(call("/video/start", "POST", payload={
                "durationSeconds": 45, "maxSize": 256, "fps": 5, "bitrate": 200000,
            }))
            if not reply.get("ok"):
                raise RuntimeError("切换测试未能启动编码器")
            owned = True

            def wait_frames(after):
                deadline = time.monotonic() + 15
                while time.monotonic() < deadline:
                    status = json.loads(call("/video/status"))["data"]
                    if status.get("error") or not status.get("running"):
                        raise RuntimeError("切换期间编码已结束: " + json.dumps(status, ensure_ascii=False))
                    if status.get("frames", 0) > after + 2 and 0 <= status.get("lastFrameAgeMs", -1) < 1500:
                        return status
                    time.sleep(0.25)
                raise RuntimeError("切换后八秒内没有恢复连续出帧")

            status = wait_frames(0)
            samples.append({"phase": "beforeSwitch", "status": status})
            for bundle in ("com.apple.Preferences", "dev.autogov.host"):
                subprocess.run(["xcrun", "simctl", "launch", "booted", bundle],
                               check=True, capture_output=True, timeout=45)
                time.sleep(1.5)
                # 从切换后的计数继续观察，不能拿切换前已产出的帧冒充恢复。
                after = json.loads(call("/video/status"))["data"].get("frames", 0)
                status = wait_frames(after)
                samples.append({"phase": bundle, "status": status})
            results["videoSwitchProbe"] = {"ok": True, "samples": samples, "deviceValidated": False}
        except Exception as exc:
            switch_error = str(exc)
            results["videoSwitchProbe"] = {"ok": False, "error": switch_error,
                                           "samples": samples, "deviceValidated": False}
        finally:
            if owned:
                stopped = json.loads(call("/video/stop", "POST"))
                if not stopped.get("ok"):
                    switch_error = switch_error or "切换测试停止编码器失败"
                results["videoAfterSwitchProbe"] = json.loads(call("/video/status"))
                if results["videoAfterSwitchProbe"].get("data", {}).get("running"):
                    switch_error = switch_error or "切换测试结束后编码器仍在运行"
            subprocess.run(["xcrun", "simctl", "launch", "booted", "dev.autogov.host"],
                           capture_output=True, timeout=45)
    else:
        results["videoSwitchProbe"] = {"skipped": True, "reason": "当前环境未通过基础编码探测",
                                       "deviceValidated": False}
    report = documents / "debugapi-smoke.json"
    report.write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8")
    if switch_error:
        raise RuntimeError("视频切换回归失败: " + switch_error)
    print("调试 HTTP 鉴权与设备/脚本入口检查通过；视频实际探测结果见 videoCaptureProbe，未输出 token")


if __name__ == "__main__":
    run(pathlib.Path(sys.argv[1]))
