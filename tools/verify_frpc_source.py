"""校验入库的官方 frp 源码；不访问网络，不执行预置二进制。"""
import hashlib
import json
from pathlib import Path


def main():
    repository = Path(__file__).resolve().parents[1]
    root = repository / "third_party/frp"
    lock = json.loads((repository / "third_party/frp-source.lock.json").read_text(encoding="utf-8"))
    if lock["commit"] != "5f575b84420a19350467290c5c2bf9f4c7b27e57":
        raise SystemExit("frp 源码提交与当前审核基线不符")
    expected = lock["files"]
    for name, digest in expected.items():
        path = root / name
        if not path.is_file() or path.is_symlink():
            raise SystemExit(f"frp 源码缺失或类型异常: {name}")
        if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
            raise SystemExit(f"frp 源码摘要不符: {name}")
    # 只允许 embed 所需的占位静态页面由构建生成，不能偷偷添加参与编译的文件。
    for path in root.rglob("*"):
        if not path.is_file():
            continue
        name = path.relative_to(root).as_posix()
        if name not in expected and name != "web/frpc/dist/index.html":
            raise SystemExit(f"frp 出现未登记文件: {name}")
    print(f"frp {lock['version']} source verified: {lock['commit']}, {len(expected)} files")


if __name__ == "__main__":
    main()
