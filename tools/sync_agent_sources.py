"""将主项目的独立 iOS Agent 及纯 Go 协议依赖同步到 iOS 构建镜像；不复制安卓实现。"""
import argparse, hashlib, json
from pathlib import Path
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--source',type=Path,default=Path(__file__).resolve().parents[2])
parser.add_argument('--check',action='store_true')
a=parser.parse_args();source=a.source.resolve();target=Path(__file__).resolve().parents[1]
if source==target or not (source/'agent/ios/runtime/session.go').is_file():
 raise SystemExit('请用 --source 指定主项目 v2 目录；构建镜像不能反向覆盖主项目')
directories=['agent/ios','agent/kernel','agent/transport','protocol','core/gesture','sdk/ios/runtime']
manifest={};mismatch=[]
for directory in directories:
 for p in sorted((source/directory).rglob('*.go')):
  rel=p.relative_to(source).as_posix();data=p.read_bytes();manifest[rel]=hashlib.sha256(data.replace(b'\r\n',b'\n')).hexdigest();dest=target/rel
  if not dest.is_file() or dest.read_bytes().replace(b'\r\n',b'\n')!=data.replace(b'\r\n',b'\n'):
   mismatch.append(rel)
   if not a.check:dest.parent.mkdir(parents=True,exist_ok=True);dest.write_bytes(data)
extra=[]
for directory in directories:
 for p in (target/directory).rglob('*.go'):
  if p.relative_to(target).as_posix() not in manifest:extra.append(p.relative_to(target).as_posix())
if extra:raise SystemExit('构建镜像含过期源码，请核对后清理：'+', '.join(extra))
if a.check and mismatch:raise SystemExit('构建镜像未同步：'+', '.join(mismatch))
if not a.check:(target/'agent-source-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n',encoding='utf-8')
print(('已检查' if a.check else '已同步')+str(len(manifest))+' 个 iOS Agent/公共依赖文件；未包含安卓 core/commands/SDK')
