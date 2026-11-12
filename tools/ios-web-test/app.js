import { AnnexB, parseVideoPacket } from './h264.mjs';

const $ = id => document.getElementById(id);
const canvas = $('canvas'), context = canvas.getContext('2d');
let connected = false, alive = false, unlocked = null, videoAbort = null, decoder = null;
let shotTimer = null, pollTimer = null, reading = Promise.resolve(), lastDraw = 0, deviceHost = 'dev.autogov.host';
let frameCount = 0, received = 0, generation = 0, needKey = true, lastKeyRequest = 0;
let writeBusy = false, gesture = null, parameters = null, videoSocket = null;

function log(text, kind = '') {
  const line = document.createElement('div'); line.className = kind;
  line.textContent = `${new Date().toLocaleTimeString()}  ${text}`; $('log').prepend(line);
  while ($('log').children.length > 120) $('log').lastChild.remove();
}
function hint(text) { $('streamHint').textContent = text; }
async function api(path, body, options = {}) {
  const target = parameters || { base: $('base').value.trim().replace(/\/$/, ''), token: $('token').value };
  const response = await fetch('/proxy' + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-IOS-Base': target.base, 'X-IOS-Token': target.token },
    body: body === undefined ? undefined : JSON.stringify(body), signal: options.signal || AbortSignal.timeout(18000),
  });
  if (options.stream && response.ok) return response;
  const data = await response.json();
  if (!response.ok || data.ok === false || data.data?.ok === false) throw Error(data.error || data.data?.error || `HTTP ${response.status}`);
  return data.data ?? data;
}
// 只将设备 JSON 操作排成短队列，避免截图与点击争用手机端闸门；视频不经过此队列。
function serial(task) { const next = reading.then(task, task); reading = next.catch(() => {}); return next; }
function invoke(method, args = {}) { return serial(() => api('/invoke', { method, args })); }
function requireConnected() { if (!connected) throw Error('请先连接设备'); }
function draw(image, width, height) {
  if (canvas.width !== width || canvas.height !== height) { canvas.width = width; canvas.height = height; }
  $('screen').style.aspectRatio = `${width}/${height}`;
  context.drawImage(image, 0, 0, width, height); lastDraw = performance.now(); $('placeholder').classList.add('hidden');
  $('frameInfo').textContent = `${width} × ${height}`;
}
async function snapshot() {
  requireConnected(); if (videoAbort) throw Error('请先停止视频，再切换截图模式');
  const frame = await serial(() => api('/screenshot'));
  const image = new Image(); image.src = `data:${frame.mime};base64,${frame.base64}`;
  await image.decode(); draw(image, frame.width, frame.height); $('viewMode').textContent = '截图模式';
}
function scheduleShot() {
  clearTimeout(shotTimer);
  if (!$('autoShot').checked || !connected || videoAbort) return;
  shotTimer = setTimeout(async () => {
    try { await snapshot(); } catch (error) { log('截图：' + error.message, 'error'); }
    scheduleShot();
  }, 1000);
}
async function refreshState() {
  requireConnected();
  const [background, video] = await Promise.all([api('/background'), api('/video/status')]);
  const on = await invoke('device.screen_on'); const lock = await invoke('device.unlocked'); const foreground = await invoke('app.foreground');
  alive = Boolean(on.on); unlocked = Boolean(lock.unlocked);
  $('lockState').textContent = !alive ? '已息屏' : unlocked ? '亮屏 · 已解锁' : '亮屏 · 锁定';
  $('hostState').textContent = ({ active: '前台', background: '后台', inactive: '切换中' })[background.appState] || background.appState || '—';
  $('audioState').textContent = background.audioPlaying ? '运行中' : background.lastError ? '异常' : '未运行';
  $('foreground').textContent = '前台应用：' + (typeof foreground === 'string' ? foreground : foreground.bundleId || '未知');
  $('rawStatus').textContent = JSON.stringify({ background, video, screenOn: alive, unlocked, foreground }, null, 2);
  if (videoAbort) {
    $('videoStats').textContent = `接收 ${(received / 1024).toFixed(1)} KB · 浏览器解码 ${frameCount} 帧 · 手机 ${video.frames ?? 0} 帧 · 最近帧 ${video.lastFrameAgeMs ?? '—'} ms`;
    if (video.error) { hint('手机编码错误：' + video.error); log(video.error, 'error'); }
  }
  return { background, video };
}
function schedulePoll() {
  clearTimeout(pollTimer); if (!connected) return;
  pollTimer = setTimeout(async () => { try { await refreshState(); } catch (error) { log('状态：' + error.message, 'error'); } schedulePoll(); }, 3000);
}
async function connect() {
  await disconnect(false);
  parameters = { base: $('base').value.trim().replace(/\/$/, ''), token: $('token').value };
  const health = await api('/health'); const info = await serial(() => api('/status'));
  connected = true; $('connectionStatus').textContent = '已连接 · PID ' + health.pid; $('connectionStatus').classList.add('online');
  $('deviceName').textContent = info.name || info.model || 'iPhone'; $('os').textContent = 'iOS ' + (info.osVersion || '—');
  const self = await invoke('app.self'); deviceHost = self.bundleId || deviceHost;
  await refreshState(); await snapshot(); hint('已连接。可在左侧真实画面上点击或拖动。'); log('设备连接成功，已获取真实截图', 'ok'); schedulePoll(); scheduleShot();
}
async function disconnect(showLog = true) {
  await stopVideo(); connected = false; parameters = null; alive = false; unlocked = null;
  clearTimeout(shotTimer); clearTimeout(pollTimer); $('connectionStatus').classList.remove('online'); $('connectionStatus').textContent = '未连接';
  if (showLog) log('已断开本地实验台；手机调试服务继续运行');
}
async function action(method, args = {}) {
  requireConnected(); if (writeBusy) throw Error('上一条操作尚未返回，请稍后重试');
  writeBusy = true;
  try {
    const result = await invoke(method, args); log(`${method}：调用成功（动作效果请以画面为准）`, 'ok');
    if (!videoAbort) { await new Promise(resolve => setTimeout(resolve, 250)); await snapshot(); }
    return result;
  } finally { writeBusy = false; }
}
function sendVideoControl(type, extra = {}) {
  if (!videoSocket || videoSocket.readyState !== WebSocket.OPEN) throw Error('视频连接未就绪');
  videoSocket.send(JSON.stringify({ type, ...extra }));
}
async function requestKey() {
  if (Date.now() - lastKeyRequest < 1200) return;
  lastKeyRequest = Date.now(); sendVideoControl('keyframe');
}
async function startVideo() {
  requireConnected(); if (!window.VideoDecoder) throw Error('此浏览器不支持 WebCodecs，请使用 Chrome/Edge 访问 localhost，或使用自动截图');
  if (videoAbort) return;
  $('autoShot').checked = false; clearTimeout(shotTimer);
  const abort = new AbortController(); videoAbort = abort; const ticket = ++generation;
  received = 0; frameCount = 0; needKey = true; lastDraw = 0; lastKeyRequest = 0;
  let configuredCodec = null, configVersion = 0, packet = null, lastSequence = 0n, lastPTS = -1;
  let wireError = null, ended = false, started = false, lastPacketAt = performance.now();
  const target = new URL(parameters.base);
  target.protocol = target.protocol === 'https:' ? 'wss:' : 'ws:';
  target.pathname = '/video/ws'; target.search = ''; target.hash = '';
  if (location.protocol === 'https:' && target.protocol === 'ws:') {
    videoAbort = null; throw Error('HTTPS 页面需要 WSS 映射地址；本地测试请从 http://127.0.0.1:18790 打开');
  }
  let socket;
  try { socket = new WebSocket(target); }
  catch (error) { videoAbort = null; throw error; }
  socket.binaryType = 'arraybuffer'; videoSocket = socket;
  decoder = new VideoDecoder({
    output(frame) { try { if (ticket === generation && !abort.signal.aborted) { draw(frame, frame.displayWidth, frame.displayHeight); frameCount++; } } finally { frame.close(); } },
    error(error) { wireError = Error('浏览器解码错误：' + error.message); socket.close(); },
  });
  const localDecoder = decoder;
  const parser = new AnnexB(frame => {
    if (abort.signal.aborted || ticket !== generation || !frame.codec) return;
    if (frame.key !== packet.key) throw Error('关键帧标记与 H.264 数据不一致');
    if (localDecoder.decodeQueueSize > 6) {
      // 不让浏览器一直播放积压旧帧；清理未解码帧，再等可独立解码的 IDR。
      localDecoder.reset(); configuredCodec = null; needKey = true;
      requestKey().catch(() => {});
    }
    if (needKey && !frame.key) return;
    if (configuredCodec !== frame.codec) {
      localDecoder.configure({ codec: frame.codec, optimizeForLatency: true }); configuredCodec = frame.codec;
    }
    needKey = false;
    localDecoder.decode(new EncodedVideoChunk({ type: frame.key ? 'key' : 'delta', timestamp: packet.pts, data: frame.data }));
  });
  const monitor = setInterval(() => {
    if (performance.now() - lastPacketAt > 18000) { wireError = Error('连续 18 秒没有收到视频帧，请检查手机编码恢复日志'); socket.close(); }
  }, 1000);
  abort.signal.addEventListener('abort', () => socket.close(), { once: true });
  hint('正在直连手机视频 WebSocket，等待首个关键帧…'); $('viewMode').textContent = 'WS / FRP · H.264';
  try {
    await new Promise((resolve, reject) => {
      socket.onopen = () => {
        if (abort.signal.aborted) { socket.close(); return; }
        // 浏览器不能自定义鉴权头；token 只在首消息里发送，不拼接进地址。
        socket.send(JSON.stringify({ type: 'start', token: parameters.token, options: {
          durationSeconds: Number($('duration').value), maxSize: Number($('maxSize').value),
          fps: Number($('fps').value), bitrate: Number($('bitrate').value),
        } }));
      };
      socket.onmessage = event => {
        if (abort.signal.aborted || ticket !== generation) return;
        try {
          if (typeof event.data === 'string') {
            const message = JSON.parse(event.data);
            if (message.type === 'error') throw Error(message.error || '手机拒绝视频连接');
            if (message.type === 'started') {
              if (message.protocol !== 'AGV1' || message.headerBytes !== 40) throw Error('手机视频协议版本不匹配');
              started = true; hint('视频通过 FRP 直接传输，可在画面上操作。'); log('手机视频 WS 已连接，不经过本地 Python 视频代理', 'ok');
            } else if (message.type === 'ended') {
              ended = true; if (message.error) throw Error(message.error);
            } else if (message.type === 'ack' && !message.ok) log('视频控制失败：' + message.error, 'error');
            return;
          }
          if (!started) throw Error('视频数据早于握手确认');
          packet = parseVideoPacket(event.data);
          if (packet.sequence !== lastSequence + 1n || packet.pts < lastPTS) throw Error('视频帧序号或时间戳异常');
          lastSequence = packet.sequence; lastPTS = packet.pts;
          if (packet.configVersion !== configVersion) {
            if (!packet.key) throw Error('编码配置变化后未从关键帧开始');
            localDecoder.reset(); configuredCodec = null; needKey = true; configVersion = packet.configVersion;
          }
          received += packet.data.length; lastPacketAt = performance.now();
          parser.push(packet.data, true); // 完整 AU，立即解码，不再等下一次网络分块猜边界。
        } catch (error) { wireError = error; socket.close(); }
      };
      socket.onerror = () => { wireError ||= Error('视频 WS 连接失败：请确认已安装新版手机端，FRP 映射到调试端口 18990'); };
      socket.onclose = () => {
        if (abort.signal.aborted) resolve();
        else if (wireError) reject(wireError);
        else if (!ended) reject(Error('视频连接意外中断，可查看手机日志后重新连接'));
        else resolve();
      };
    });
    if (abort.signal.aborted || ticket !== generation) return;
    if (localDecoder.state === 'configured') await localDecoder.flush();
    if (!frameCount) throw Error('连接结束，但浏览器没有解码出画面');
    hint(`本次限时采集结束，共显示 ${frameCount} 帧。`); log(`WS 视频结束：${frameCount} 帧`, 'ok');
  } catch (error) {
    if (!abort.signal.aborted && ticket === generation) { lastDraw = 0; hint('视频失败：' + error.message); log('视频：' + error.message, 'error'); }
  } finally {
    clearInterval(monitor); socket.close();
    if (localDecoder.state !== 'closed') localDecoder.close();
    if (videoSocket === socket) videoSocket = null;
    if (videoAbort === abort) { videoAbort = null; decoder = null; scheduleShot(); }
  }
}
async function stopVideo() {
  const current = videoAbort; if (!current) return;
  videoAbort = null; generation++; current.abort();
  // 关闭本页面的 WS，手机端释放本连接的编码器；不通过全局 stop 抢停其他会话。
  if (decoder && decoder.state !== 'closed') decoder.close(); decoder = null;
  hint('视频已停止，可刷新截图。'); $('viewMode').textContent = '视频已停止'; log('已关闭本页面视频连接');
}
function bind(id, task) { const button = $(id); button.addEventListener('click', () => { button.disabled = true; Promise.resolve().then(task).catch(error => { log(error.message, 'error'); hint(error.message); }).finally(() => { button.disabled = false; }); }); }
bind('connect', connect); bind('disconnect', disconnect); bind('refresh', refreshState);
bind('capture', async () => { await stopVideo(); await snapshot(); }); bind('startVideo', startVideo); bind('stopVideo', stopVideo);
bind('keyframe', requestKey); bind('applyBitrate', () => { sendVideoControl('bitrate', { bitrate: Number($('bitrate').value) }); log('已请求调整码率'); });
bind('openSettings', () => action('app.launch', { bundleId: 'com.apple.Preferences' })); bind('openHost', () => action('app.launch', { bundleId: deviceHost }));
bind('openApp', () => { const bundleId = $('bundle').value.trim(); if (!bundleId) throw Error('请填写 bundleId'); return action('app.launch', { bundleId }); });
bind('swipeUp', () => action('motion.swipe', { x1: .5, y1: .75, x2: .5, y2: .3, durationMs: 450 }));
bind('swipeDown', () => action('motion.swipe', { x1: .5, y1: .3, x2: .5, y2: .75, durationMs: 450 }));
bind('sendText', () => { const text = $('text').value; if (!text.trim()) throw Error('请输入文字'); return action('ime.text', { text }); });
bind('clearLog', () => { $('log').replaceChildren(); });
for (const button of document.querySelectorAll('[data-method]')) button.addEventListener('click', () => action(button.dataset.method, JSON.parse(button.dataset.args || '{}')).catch(error => log(error.message, 'error')));
$('autoShot').addEventListener('change', async () => { if ($('autoShot').checked) await stopVideo(); scheduleShot(); });
function point(event) {
  const rect = canvas.getBoundingClientRect();
  return { x: Math.max(0, Math.min(1, (event.clientX - rect.left) / rect.width)), y: Math.max(0, Math.min(1, (event.clientY - rect.top) / rect.height)) };
}
$('screen').addEventListener('pointerdown', event => {
  if (!connected || !$('allowTouch').checked || event.button !== 0) return;
  if (!alive || unlocked === false) { log('手机已息屏或锁定，请唤醒并手动解锁', 'error'); return; }
  if (!lastDraw || performance.now() - lastDraw > 5000) { log('画面可能已过期，请先刷新截图', 'error'); return; }
  const p = point(event); gesture = { ...p, start: performance.now(), pointer: event.pointerId, points: [p] }; $('screen').setPointerCapture(event.pointerId);
});
$('screen').addEventListener('pointermove', event => {
  if (!gesture || event.pointerId !== gesture.pointer) return;
  const p = point(event); gesture.points.push(p);
  const rect = canvas.getBoundingClientRect(); $('trace').setAttribute('viewBox', `0 0 ${rect.width} ${rect.height}`);
  $('tracePath').setAttribute('d', gesture.points.map((a, i) => `${i ? 'L' : 'M'}${a.x * rect.width},${a.y * rect.height}`).join(' '));
});
$('screen').addEventListener('pointerup', event => {
  if (!gesture || event.pointerId !== gesture.pointer) return;
  const start = gesture; gesture = null; $('tracePath').setAttribute('d', ''); const end = point(event), duration = performance.now() - start.start;
  $('gestureInfo').textContent = `操作位置：${end.x.toFixed(3)}, ${end.y.toFixed(3)}`;
  const moved = Math.hypot(end.x - start.x, end.y - start.y) > .025;
  const method = moved ? 'motion.swipe' : duration > 500 ? 'motion.longpress' : 'motion.tap';
  const args = moved ? { x1: start.x, y1: start.y, x2: end.x, y2: end.y, durationMs: Math.round(Math.max(180, Math.min(1800, duration))) } : { x: end.x, y: end.y, ...(duration > 500 ? { durationMs: Math.round(Math.min(3000, duration)) } : {}) };
  action(method, args).catch(error => log(error.message, 'error'));
});
$('screen').addEventListener('pointercancel', () => { gesture = null; $('tracePath').setAttribute('d', ''); });
window.addEventListener('pagehide', () => { videoAbort?.abort(); clearTimeout(shotTimer); clearTimeout(pollTimer); });
$('codec').textContent = window.VideoDecoder ? 'WebCodecs 可用' : '仅支持截图模式';
if (location.protocol === 'file:') hint('请运行 start.bat，从 localhost 打开本页面；直接双击 HTML 无法跨域访问手机。');
log('实验台已就绪，尚未向手机下发操作。');
