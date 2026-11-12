"""独立 iOS HTML 实验台：仅本机同源转发，不接入 Agent，不修改手机端 CORS。"""
import argparse
import json
import socket
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

ROOT = Path(__file__).resolve().parent
GET_PATHS = {"/health", "/status", "/background", "/api", "/worker", "/screenshot", "/ui-tree", "/video/status", "/video/stream"}
POST_PATHS = {"/invoke", "/video/start", "/video/stop", "/video/keyframe", "/video/bitrate"}
METHODS = {"app.launch", "app.foreground", "app.self", "device.screen_on", "device.unlocked", "device.button", "device.wake", "device.lock", "motion.tap", "motion.longpress", "motion.swipe", "motion.pinch", "ime.text"}
STATIC = {"/": ("index.html", "text/html"), "/index.html": ("index.html", "text/html"), "/app.js": ("app.js", "text/javascript"), "/h264.mjs": ("h264.mjs", "text/javascript")}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None  # 不把设备凭据带到重定向后的未知地址。


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_):
        pass  # 不输出请求头、设备 token 或输入内容。

    def send_bytes(self, status, data, mime="application/json"):
        self.send_response(status)
        self.send_header("Content-Type", mime + "; charset=utf-8")
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.end_headers()
        self.wfile.write(data)

    def error(self, status, message):
        self.send_bytes(status, json.dumps({"ok": False, "error": message}, ensure_ascii=False).encode())

    def do_GET(self):
        self.handle_request()

    def do_POST(self):
        self.handle_request()

    def handle_request(self):
        port = self.server.server_port
        hosts = {f"127.0.0.1:{port}", f"localhost:{port}"}
        if self.headers.get("Host") not in hosts:
            return self.error(403, "只允许本机访问")
        origin = self.headers.get("Origin")
        if origin and origin not in {"http://" + host for host in hosts}:
            return self.error(403, "拒绝跨站请求")
        parsed = urllib.parse.urlsplit(self.path)
        if self.command == "GET" and parsed.path in STATIC:
            name, mime = STATIC[parsed.path]
            return self.send_bytes(200, (ROOT / name).read_bytes(), mime)
        if not parsed.path.startswith("/proxy/"):
            return self.error(404, "未知路径")
        path = parsed.path[len("/proxy"):]
        allowed = GET_PATHS if self.command == "GET" else POST_PATHS
        if path not in allowed:
            return self.error(403, "不在实验台接口清单中")
        try:
            target = urllib.parse.urlsplit(self.headers.get("X-IOS-Base", "http://49.235.187.13:18990"))
            if target.scheme not in {"http", "https"} or not target.hostname or target.username or target.password or target.path not in {"", "/"} or target.query or target.fragment:
                raise ValueError("设备地址必须为 http(s)://主机:端口，不包含路径或账号")
            target.port  # 同时验证端口。
            token = self.headers.get("X-IOS-Token", "")
            if not token or len(token) > 1024:
                raise ValueError("请填写调试 token")
            length = int(self.headers.get("Content-Length", "0"))
            if length < 0 or length > 64 << 10:
                raise ValueError("实验台请求不能超过 64 KiB")
            body = self.rfile.read(length) if self.command == "POST" else None
            if path == "/invoke":
                value = json.loads(body or b"{}")
                if not isinstance(value, dict) or value.get("method") not in METHODS:
                    return self.error(403, "该设备操作未在实验台开放")
            query = urllib.parse.parse_qs(parsed.query)
            if "token" in query:
                raise ValueError("页面使用请求头鉴权，不把 token 放入 URL")
            url = urllib.parse.urlunsplit((target.scheme, target.netloc, path, parsed.query, ""))
        except (ValueError, TypeError, json.JSONDecodeError):
            return self.error(400, "设备地址、请求长度或 JSON 无效")
        request = urllib.request.Request(url, data=body, method=self.command, headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
        try:
            with opener.open(request, timeout=12 if path != "/video/stream" else 15) as upstream:
                if path == "/video/stream":
                    self.send_response(200)
                    self.send_header("Content-Type", "video/h264")
                    self.send_header("Cache-Control", "no-store")
                    self.send_header("Connection", "close")
                    self.end_headers()
                    self.close_connection = True
                    self.connection.settimeout(4)
                    # read1 不等待填满大缓冲，避免为低码率静态页面人为增加延迟。
                    while True:
                        chunk = upstream.read1(16384)
                        if not chunk:
                            break
                        self.wfile.write(chunk)
                        self.wfile.flush()
                else:
                    data = upstream.read((8 << 20) + 1)
                    if len(data) > 8 << 20:
                        return self.error(502, "设备返回内容超过实验台上限")
                    self.send_bytes(200, data)
        except urllib.error.HTTPError as exc:
            self.send_bytes(exc.code, exc.read(64 << 10))
        except (BrokenPipeError, ConnectionResetError):
            pass  # 浏览器停止视频时关闭上游连接，让手机释放对应采集器。
        except (urllib.error.URLError, TimeoutError, socket.timeout, OSError):
            if path == "/video/stream" and self.close_connection:
                return
            self.error(502, "连接手机失败或超时，请检查 frpc、映射地址和手机状态")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=18790)
    args = parser.parse_args()
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    server.daemon_threads = True
    print(f"iOS 实验台：http://127.0.0.1:{server.server_port}/", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
