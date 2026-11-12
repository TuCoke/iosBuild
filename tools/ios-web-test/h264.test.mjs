import test from 'node:test';
import assert from 'node:assert/strict';
import { AnnexB, parseVideoPacket } from './h264.mjs';

const au = new Uint8Array([0, 0, 0, 1, 0x67, 0x42, 0, 0x1f, 0, 0, 0, 1, 0x68, 0xce, 6, 0, 0, 0, 1, 0x65, 0x80]);
function message() {
  const data = new Uint8Array(40 + au.length), view = new DataView(data.buffer);
  data.set([65, 71, 86, 49]); data[4] = 1;
  view.setBigUint64(8, 1n); view.setBigUint64(16, 123456n);
  view.setUint32(24, 332); view.setUint32(28, 720);
  view.setUint32(32, au.length); view.setUint32(36, 1); data.set(au, 40);
  return data.buffer;
}

test('WS 保留手机 PTS 和完整 AU，不用固定帧率猜时间戳', () => {
  const packet = parseVideoPacket(message());
  assert.equal(packet.sequence, 1n); assert.equal(packet.pts, 123456);
  assert.equal(packet.width, 332); assert.equal(packet.configVersion, 1);
  const frames = [];
  new AnnexB(frame => frames.push(frame)).push(packet.data, true);
  assert.equal(frames.length, 1); assert.equal(frames[0].key, true);
  assert.equal(frames[0].codec, 'avc1.42001f'); assert.deepEqual(frames[0].data, au);
});

test('截断、版本、长度、时间戳和配置头异常必须拒绝', () => {
  assert.throws(() => parseVideoPacket(new ArrayBuffer(39)));
  for (const mutate of [
    view => view.setUint8(0, 0), view => view.setUint8(4, 2),
    view => view.setUint32(32, 999), view => view.setUint32(36, 0),
    view => view.setBigUint64(16, BigInt(Number.MAX_SAFE_INTEGER) + 1n),
  ]) {
    const data = message(); mutate(new DataView(data)); assert.throws(() => parseVideoPacket(data));
  }
});

test('HTTP 兼容解析仍支持起始码和 NAL 被网络拆分', () => {
  const frames = [], parser = new AnnexB(frame => frames.push(frame));
  for (const byte of au) parser.push(new Uint8Array([byte]));
  parser.push(new Uint8Array(), true);
  assert.equal(frames.length, 1); assert.deepEqual(frames[0].data, au);
});
