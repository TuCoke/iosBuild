// 当前 SDK 使用 Baseline、无 B 帧。网络分块不等于 NAL，更不等于完整视频帧。
export function starts(bytes) {
  const result = [];
  for (let i = 0; i + 2 < bytes.length; i++) {
    if (bytes[i] || bytes[i + 1]) continue;
    const length = bytes[i + 2] === 1 ? 3 : bytes[i + 2] === 0 && bytes[i + 3] === 1 ? 4 : 0;
    if (length) { result.push([i, length]); i += length - 1; }
  }
  return result;
}

function firstSlice(nal) {
  // first_mb_in_slice 的 Exp-Golomb 值为 0 时，第一个 RBSP 位就是 1。
  return nal.length > 1 && (nal[1] & 0x80) !== 0;
}

export class AnnexB {
  constructor(onFrame) { this.onFrame = onFrame; this.buffer = new Uint8Array(); this.nals = []; this.vcl = false; this.size = 0; this.sps = null; this.pps = null; }
  push(chunk, final = false) {
    if (this.buffer.length + chunk.length > 4 * 1024 * 1024) throw Error('H.264 未完成 NAL 超过 4 MiB');
    const data = new Uint8Array(this.buffer.length + chunk.length);
    data.set(this.buffer); data.set(chunk, this.buffer.length);
    const list = starts(data);
    for (let i = 0; i + 1 < list.length; i++) this.nal(data.slice(list[i][0] + list[i][1], list[i + 1][0]));
    if (final && list.length) {
      const last = list.at(-1); this.nal(data.slice(last[0] + last[1])); this.buffer = new Uint8Array(); this.flush();
    } else if (list.length) this.buffer = data.slice(list.at(-1)[0]);
    else this.buffer = data;
  }
  nal(nal) {
    if (!nal.length) return;
    const type = nal[0] & 31;
    const isVcl = type === 1 || type === 5;
    if (this.vcl && ((isVcl && firstSlice(nal)) || [6, 7, 8, 9].includes(type))) this.flush();
    if (type === 7) this.sps = nal;
    if (type === 8) this.pps = nal;
    this.nals.push(nal); this.size += nal.length + 4;
    if (this.size > 4 * 1024 * 1024) throw Error('单帧超过 4 MiB');
    if (isVcl) this.vcl = true;
  }
  flush() {
    if (!this.vcl) return;
    const key = this.nals.some(n => (n[0] & 31) === 5);
    const nals = [...this.nals];
    if (key && this.sps && this.pps && !nals.some(n => (n[0] & 31) === 7)) nals.unshift(this.sps, this.pps);
    const data = new Uint8Array(nals.reduce((n, b) => n + b.length + 4, 0)); let offset = 0;
    for (const nal of nals) { data.set([0, 0, 0, 1], offset); data.set(nal, offset + 4); offset += nal.length + 4; }
    const codec = this.sps?.length >= 4 ? 'avc1.' + [...this.sps.slice(1, 4)].map(n => n.toString(16).padStart(2, '0')).join('') : null;
    this.onFrame({ data, key, codec }); this.nals = []; this.vcl = false; this.size = 0;
  }
}

// 手机端 WS 一条消息就是完整访问单元；校验头后沿用 Annex B 参数解析和解码。
export function parseVideoPacket(buffer) {
  if (!(buffer instanceof ArrayBuffer) || buffer.byteLength < 40 || buffer.byteLength > 40 + (4 << 20)) throw Error('视频消息长度无效');
  const bytes = new Uint8Array(buffer), view = new DataView(buffer);
  if (String.fromCharCode(...bytes.slice(0, 4)) !== 'AGV1' || (bytes[4] & ~1) || bytes[5] || bytes[6] || bytes[7]) throw Error('不支持的视频协议');
  const sequence = view.getBigUint64(8), pts = view.getBigUint64(16);
  const width = view.getUint32(24), height = view.getUint32(28), length = view.getUint32(32), configVersion = view.getUint32(36);
  if (!sequence || pts > BigInt(Number.MAX_SAFE_INTEGER) || !width || !height || width > 8192 || height > 8192 || !configVersion || !length || length !== bytes.length - 40) throw Error('视频帧头无效');
  return { sequence, pts: Number(pts), width, height, configVersion, key: Boolean(bytes[4] & 1), data: bytes.subarray(40) };
}
