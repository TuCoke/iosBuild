#!/usr/bin/env python3
"""原生资源获取与校验：只接受锁定摘要，不自动下载或执行未知二进制。"""
import argparse
import hashlib
import json
import os
import shutil
import struct
import tempfile
import urllib.parse
import urllib.request
from pathlib import Path, PurePosixPath

MAX_BYTES = 256 * 1024 * 1024


def image_platform(path):
    with path.open("rb") as stream:
        header = stream.read(32)
        if len(header) != 32 or header[:4] != b"\xcf\xfa\xed\xfe":
            raise ValueError(f"不是薄 arm64 Mach-O：{path.name}")
        _, cpu, _, kind, count, size, _, _ = struct.unpack("<8I", header)
        if cpu != 0x100000c or kind not in (2, 6) or size > 4 * 1024 * 1024 or count > 65536:
            raise ValueError(f"不支持的 Mach-O：{path.name}")
        loads = stream.read(size)
    offset = 0
    legacy = False
    for _ in range(count):
        if offset + 8 > len(loads):
            raise ValueError("Mach-O 载荷截断")
        command, length = struct.unpack_from("<II", loads, offset)
        if length < 8 or offset + length > len(loads):
            raise ValueError("Mach-O 载荷长度无效")
        if command == 0x32:
            if length < 24:
                raise ValueError("平台载荷截断")
            return {2: "ios", 7: "ios-simulator"}.get(struct.unpack_from("<I", loads, offset + 8)[0], "other")
        legacy |= command == 0x25
        offset += length
    return "ios" if legacy else "unknown"


def validate(path, item):
    if not path.is_file() or path.is_symlink():
        raise ValueError("文件缺失或为软链接")
    if path.stat().st_size != item["bytes"] or path.stat().st_size > MAX_BYTES:
        raise ValueError("文件大小不符")
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    if digest.hexdigest() != item["sha256"]:
        raise ValueError("SHA256 不符")
    if item["kind"] == "macho" and image_platform(path) != item["platform"]:
        raise ValueError("Mach-O 平台不符")


def target_path(root, item):
    relative = PurePosixPath(item["path"])
    if relative.is_absolute() or ".." in relative.parts or "\\" in item["path"]:
        raise ValueError("资源路径越界")
    target = root.joinpath(*relative.parts)
    if not target.resolve().is_relative_to(root.resolve()):
        raise ValueError("资源目标或软链接越界")
    return target


def acquire(target, item, source_dir):
    target.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=".resource-", dir=target.parent)
    os.close(fd)
    temporary = Path(temporary)
    try:
        if source_dir:
            source = source_dir / item["source"]
            if not source.resolve().is_relative_to(source_dir.resolve()) or source.is_symlink():
                raise ValueError("资源来源越界")
            if source.stat().st_size > MAX_BYTES:
                raise ValueError("来源文件过大")
            shutil.copyfile(source, temporary)
        else:
            url = item.get("url", "")
            if urllib.parse.urlsplit(url).scheme != "https":
                raise ValueError("没有锁定的 HTTPS 来源；请提供 --source-dir")
            with urllib.request.urlopen(url, timeout=60) as response, temporary.open("wb") as output:
                if urllib.parse.urlsplit(response.geturl()).scheme != "https":
                    raise ValueError("禁止降级到非 HTTPS 来源")
                total = 0
                while block := response.read(1024 * 1024):
                    total += len(block)
                    if total > item["bytes"] or total > MAX_BYTES:
                        raise ValueError("下载超过锁定大小")
                    output.write(block)
        validate(temporary, item)
        os.replace(temporary, target)
    finally:
        temporary.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--manifest", type=Path)
    parser.add_argument("--source-dir", type=Path, help="受信任的预编译资源目录，按 manifest.source 定位")
    parser.add_argument("--fetch", action="store_true", help="缺件时从指定来源获取，不运行资源")
    parser.add_argument("--groups", default="opencv,recognition", help="需交付的能力组，逗号分隔")
    args = parser.parse_args()
    manifest = args.manifest or args.root / "host/ios/native-resources.lock.json"
    config = json.loads(manifest.read_text(encoding="utf-8"))
    if config.get("version") != 1:
        parser.error("资源锁格式不支持")
    groups = set(args.groups.split(","))
    available = {item["group"] for item in config["resources"]}
    if not groups <= available:
        parser.error(f"未知能力组：{sorted(groups - available)}")
    errors = []
    for item in config["resources"]:
        if item["group"] not in groups:
            continue
        try:
            target = target_path(args.root, item)
            try:
                validate(target, item)
            except (ValueError, OSError):
                if not args.fetch:
                    raise
                acquire(target, item, args.source_dir)
            print(f"已校验 {item['path']}")
        except (ValueError, OSError) as error:
            errors.append(f"{item['path']}: {error}")
    if errors:
        raise SystemExit("原生资源交付失败：\n" + "\n".join(errors))


if __name__ == "__main__":
    main()
