"""检查 iOS Agent 的传递依赖，防止误引入安卓实现。只读，不启动设备或服务。"""
import os, subprocess, hashlib, json
from pathlib import Path
root=Path(__file__).resolve().parents[1]
manifest=json.loads((root/'agent-source-manifest.json').read_text(encoding='utf-8'))
for name,digest in manifest.items():
 p=root/name
 if not p.is_file() or hashlib.sha256(p.read_bytes().replace(b'\r\n',b'\n')).hexdigest()!=digest:raise SystemExit('iOS 构建镜像源码与同步清单不一致：'+name)
env={**os.environ,'GOWORK':'off','CGO_ENABLED':'0'}
packages=subprocess.check_output(['go','list','-deps','./agent/ios/...'],cwd=root,env=env,text=True).splitlines()
forbidden=[]
for name in packages:
 if name in ['autogov1/v2/agent/core','autogov1/v2/agent/commands','autogov1/v2/agent/internal/app']:
  forbidden.append(name)
 # sdk/imaging 是已有纯 Go 图像算法，供两平台共用，不含安卓驱动。
 if name.startswith('autogov1/v2/sdk/') and not name.startswith('autogov1/v2/sdk/ios/') and name!='autogov1/v2/sdk/imaging':
  forbidden.append(name)
if forbidden:raise SystemExit('iOS Agent 引入了安卓能力：'+', '.join(sorted(set(forbidden))))
print('iOS Agent 依赖隔离检查通过')
