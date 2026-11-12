// Package protocol 定义各端共用的通信类型，不依赖服务端或设备实现。
package protocol

import (
	"autogov1/v2/core/gesture"
	"encoding/json"
	"fmt"
)

// 二进制帧类型(帧首字节)。
// Agent -> Server:  [type:1][payload]
// Server -> Client: [type:1][idLen:1][deviceId:idLen][payload]
const (
	BinVideo      byte = 0x00 // H.264 视频流
	BinThumbnail  byte = 0x01 // JPEG 缩略图(网格态)
	BinScreenshot byte = 0x02 // JPEG 按需全屏截图(自动化识别用)
)

// AgentMsg 是设备端(Agent)通过 JSON 文本帧上报的消息。
type AgentMsg struct {
	Type string `json:"type"` // register | heartbeat | result | *_result

	// register
	Kernel     *KernelIdentity `json:"kernel,omitempty"`
	DeviceID   string          `json:"deviceId,omitempty"`
	Name       string          `json:"name,omitempty"`
	Model      string          `json:"model,omitempty"`
	Width      int             `json:"w,omitempty"`
	Height     int             `json:"h,omitempty"`
	Platform   string          `json:"platform,omitempty"`
	OSVersion  string          `json:"osVersion,omitempty"`
	AndroidVer string          `json:"androidVer,omitempty"`
	Features   []string        `json:"features,omitempty"`
	SessionID  string          `json:"sessionId,omitempty"`
	Commands   []string        `json:"commands,omitempty"`

	// heartbeat
	Battery int    `json:"battery,omitempty"`
	FgApp   string `json:"fgApp,omitempty"`

	// result(命令执行结果)
	ID    string          `json:"id,omitempty"`
	OK    bool            `json:"ok,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	State string          `json:"state,omitempty"`

	// findtap_result
	Msg   string  `json:"msg,omitempty"`
	X     float64 `json:"x,omitempty"`
	Y     float64 `json:"y,omitempty"`
	Score float64 `json:"score,omitempty"`

	// yolo_result / ui_result:原样透传的数组
	Boxes json.RawMessage `json:"boxes,omitempty"`
	Nodes json.RawMessage `json:"nodes,omitempty"`

	// webrtc 信令(agent 作为发布端 offerer):
	// Type=webrtc_offer 带 SDP;Type=webrtc_ice 带候选(webrtc.ICECandidateInit 的 JSON)。
	SDP string          `json:"sdp,omitempty"`
	ICE json.RawMessage `json:"ice,omitempty"`
}

// Cmd 是下发给 Agent 的命令,也是 Client 发起命令时携带的载荷。
type Cmd struct {
	Frames     []gesture.Frame `json:"frames,omitempty"`     // gesture: 有界多指轨迹，空帧表示全部抬起
	Fence      int64           `json:"fence,omitempty"`      // 服务端持久预约产生的设备写入栅栏，客户端不能指定
	Type       string          `json:"type"`                 // 恒为 "cmd"
	ID         string          `json:"id,omitempty"`         // 请求-响应配对用
	SessionID  string          `json:"sessionId,omitempty"`  // 设备生成的连接代次，阻止旧连接命令进入新会话
	DeadlineAt int64           `json:"deadlineAt,omitempty"` // 执行开始的最晚时间，Unix 毫秒
	RequestID  string          `json:"requestId,omitempty"`  // 取消及结果查询所指向的原请求

	Cmd string `json:"cmd"` // touch|tap|swipe|text|key|video_start|video_stop|screenshot|findtap|open_app|yolo|ui_click|ui_dump

	// 坐标一律归一化 0..1,由 Agent 侧乘以真实分辨率
	X      float64 `json:"x,omitempty"`
	Y      float64 `json:"y,omitempty"`
	X2     float64 `json:"x2,omitempty"`
	Y2     float64 `json:"y2,omitempty"`
	Dur    int     `json:"dur,omitempty"`    // swipe 时长 ms
	Action int     `json:"action,omitempty"` // touch: 0=down 1=up 2=move

	Text   string `json:"text,omitempty"`   // text 输入内容
	Key    int    `json:"key,omitempty"`    // key 键码
	Script string `json:"script,omitempty"` // script 脚本名/内容

	Tpl    string  `json:"tpl,omitempty"`    // findtap: base64 PNG 模板
	Thresh float64 `json:"thresh,omitempty"` // findtap/yolo: 阈值
	Tap    bool    `json:"tap,omitempty"`    // findtap: 命中是否点击
	Pkg    string  `json:"pkg,omitempty"`    // open_app/app_*: 包名
	Path   string  `json:"path,omitempty"`   // app_install: APK 路径 / 文件路径
	Value  int     `json:"value,omitempty"`  // set_brightness/set_volume/vibrate: 数值
	Stream int     `json:"stream,omitempty"` // get_volume/set_volume: 音量流 3=媒体 5=通知 4=闹钟 2=铃声
	On     bool    `json:"on,omitempty"`     // 开关类(screen 等)
	Sys    bool    `json:"sys,omitempty"`    // app_list: 是否含系统应用
	Status int     `json:"status,omitempty"` // set_battery: 电池状态 1没充2充3没插4不充5满
	By     string  `json:"by,omitempty"`     // ui_click: 选择器类型
	Val    string  `json:"val,omitempty"`    // ui_click: 选择器值 / find_text: 目标文字
	Color  string  `json:"color,omitempty"`  // find_color/cmp_color/color_count: 颜色串

	// 投屏参数(video_start / video_cfg)。0 = 让 agent 用它自己的默认值。
	//
	// 【omitempty + 老 agent 会忽略未知字段】所以服务端可以先上,内核慢慢推 ——
	// 老 agent 收到这两个字段直接跳过,行为和现在完全一样。
	Bitrate int     `json:"bitrate,omitempty"` // H.264 码率(bps)
	VWidth  int     `json:"vwidth,omitempty"`  // 编码宽度上限,按设备比例等比缩
	VFps    int     `json:"vfps,omitempty"`    // 编码帧率;建流时定死,改它要重开流
	Sim     float64 `json:"sim,omitempty"`     // find_color 等: 相似度 0.1..1
	Dir     int     `json:"dir,omitempty"`     // find_color: 查找方向 0-3
	Intent  *Intent `json:"intent,omitempty"`  // start_activity: Intent 规格

	// 投屏模式(video_start):"webrtc"=让 agent 用 WebRTC 发布画面;""/"ws"=沿用 WS 二进制上传。
	// omitempty + 老 agent 忽略未知字段 → 服务端可先上,老内核仍走 WS。
	Mode string `json:"mode,omitempty"`
	// webrtc 信令(server→agent):Cmd 取 "webrtc_answer" 带 SDP / "webrtc_ice" 带候选。
	SDP string          `json:"sdp,omitempty"`
	ICE json.RawMessage `json:"ice,omitempty"`

	// video_start(mode=webrtc):把 STUN/TURN 服务器下发给 agent 的发布端 —— 手机在封 UDP 的
	// 网络下靠 TURN 中继才能连上 SFU。凭证只配在 server(env),随命令发下来,agent 不硬编。
	ICEServers []ICEServer `json:"iceServers,omitempty"`
}

// ICEServer 和浏览器原生 RTCIceServer 同形(STUN 无需账号;TURN 要 username/credential)。
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// Intent 是 start_activity 的 Intent 规格(am start)。
type Intent struct {
	Action     string            `json:"action,omitempty"`
	Data       string            `json:"data,omitempty"`
	Mime       string            `json:"mime,omitempty"`
	Pkg        string            `json:"pkg,omitempty"`
	Class      string            `json:"class,omitempty"`
	Categories []string          `json:"cats,omitempty"`
	Extras     map[string]string `json:"extras,omitempty"`
	Flags      []string          `json:"flags,omitempty"`
}

// ClientMsg 是控制端(Client)发来的消息。
type ClientMsg struct {
	Type      string   `json:"type"` // subscribe|unsubscribe|cmd|batch|task|agent|yolo|findtap|launch|ui_click|ui_dump
	DeviceID  string   `json:"deviceId,omitempty"`
	DeviceIDs []string `json:"deviceIds,omitempty"`
	Cmd       *Cmd     `json:"cmd,omitempty"`

	// task/agent: 图片识别→点击 / 多步执行
	Instruction string `json:"instruction,omitempty"`
	Periodic    bool   `json:"periodic,omitempty"` // brain: 周期/持续模式(重复动作不中止)
	// Model 这一次用哪个模型(模型管理里的 id)。空 = 用租户的默认模型。
	// 【按次传而不是全局设】:换模型不该影响正在跑的别的任务,
	// 而"想拿两个模型跑同一件事比一比"是最常见的用法。
	Model      string `json:"model,omitempty"`
	Recognizer string `json:"recognizer,omitempty"`
	Tap        bool   `json:"tap,omitempty"`

	// findtap: 模板(base64 PNG)
	Tpl    string  `json:"tpl,omitempty"`
	Thresh float64 `json:"thresh,omitempty"`

	// launch: App 显示名或包名
	App string `json:"app,omitempty"`
	Pkg string `json:"pkg,omitempty"`

	// ui_click / ui_dump: 无障碍选择器
	By  string `json:"by,omitempty"`
	Val string `json:"val,omitempty"`

	// start_activity: Intent 规格(open_url 用 Val 传 URL)
	Intent *Intent `json:"intent,omitempty"`

	// flow_run: 回放脚本模板名 + 变量槽的值(内容)
	Flow   string            `json:"flow,omitempty"`
	Params map[string]string `json:"params,omitempty"`

	// video_cfg: 控制台改这台设备的投屏参数。
	//
	// 【这是设备级设置,不是观众级】一台设备只有一条编码流:A 调高码率,
	// 同时在 4G 上看的 B 会一起变。要做成"每人自己的画质"得服务端转码或多档流,
	// 成本高一个量级 —— 界面上要写明"会影响所有正在观看的人"。
	Bitrate int `json:"bitrate,omitempty"`
	VWidth  int `json:"vwidth,omitempty"`

	// 任务编排(task_create / task_stop / task_retry)
	Contents []string `json:"contents,omitempty"` // 内容池:一对一分给设备,用完即止
	VarName  string   `json:"varName,omitempty"`  // 内容注入到脚本的哪个变量槽(空=第一个)
	JobID    string   `json:"jobId,omitempty"`

	// webrtc 信令(观看端):Type=webrtc_start 请求订阅 / webrtc_answer 带 SDP / webrtc_ice 带候选。
	SDP string          `json:"sdp,omitempty"`
	ICE json.RawMessage `json:"ice,omitempty"`
}

// DeviceInfo 是下发给 Client 的设备摘要。
type DeviceInfo struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Model      string   `json:"model"`
	Width      int      `json:"w"`
	Height     int      `json:"h"`
	Online     bool     `json:"online"`
	Battery    int      `json:"battery"`
	FgApp      string   `json:"fgApp"`
	Platform   string   `json:"platform,omitempty"`
	OSVersion  string   `json:"osVersion,omitempty"`
	AndroidVer string   `json:"androidVer,omitempty"` // 安卓版本(register 上报)
	Features   []string `json:"features,omitempty"`
	Commands   []string `json:"commands,omitempty"` // 实际设备支持的命令，供调用方预检
	Conn       string   `json:"conn,omitempty"`     // 连接类型:adb|lan|wan
	Addr       string   `json:"addr,omitempty"`     // 客户端远端地址 ip:port
	Group      string   `json:"group,omitempty"`    // 分组(来自设备档案)
	Note       string   `json:"note,omitempty"`     // 备注(来自设备档案)
}

// LogEntry 是一条后台事件日志。
type LogEntry struct {
	ID string `json:"id,omitempty"`
	At int64  `json:"at,omitempty"`
	// 投递元数据由服务端维护，不从客户端载荷读取。
	TenantID string `json:"-"`
	ActorID  string `json:"-"`
	Time     string `json:"time"`               // HH:MM:SS
	Level    string `json:"level"`              // info | ok | warn | err
	DeviceID string `json:"deviceId,omitempty"` // 关联设备(可空)
	Text     string `json:"text"`               // 人类可读事件描述
}

// ServerMsg 是下发给 Client 的 JSON 消息。
type ServerMsg struct {
	Type    string       `json:"type"` // devices|result|task_result|agent_result|findtap_result|yolo_result|ui_result|log|logs
	Devices []DeviceInfo `json:"devices,omitempty"`

	// 事件日志
	Log  *LogEntry  `json:"log,omitempty"`
	Logs []LogEntry `json:"logs,omitempty"`

	// result 透传
	DeviceID string          `json:"deviceId,omitempty"`
	ID       string          `json:"id,omitempty"`
	OK       bool            `json:"ok,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`

	// brain_result:结构化结局(为什么停:finished/stuck/max_steps/takeover/canceled/…)。
	// 有了它,任务完成通知/成功率统计/前端分类才不必只靠一个 ok 布尔去猜。
	Outcome string `json:"outcome,omitempty"`

	// task_result / findtap_result: 识别结果
	Msg   string  `json:"msg,omitempty"`
	X     float64 `json:"x,omitempty"`
	Y     float64 `json:"y,omitempty"`
	Label string  `json:"label,omitempty"`
	Score float64 `json:"score,omitempty"`

	// yolo_result / ui_result: 原样透传的数组
	Boxes json.RawMessage `json:"boxes,omitempty"`
	Nodes json.RawMessage `json:"nodes,omitempty"`

	// type=tasks:任务编排状态快照(tasks 包序列化后原样透传;
	// proto 是最底层包,不能反向依赖 tasks,所以用 RawMessage)
	Jobs json.RawMessage `json:"jobs,omitempty"`

	// webrtc 信令(server→观看端):Type=webrtc_offer 带 SDP / webrtc_ice 带候选。
	SDP string          `json:"sdp,omitempty"`
	ICE json.RawMessage `json:"ice,omitempty"`
}

// KeyName 把常见 Android keycode 翻成可读名(日志用)。
func KeyName(code int) string {
	switch code {
	case 3:
		return "主页(HOME)"
	case 4:
		return "返回(BACK)"
	case 187:
		return "多任务(RECENTS)"
	case 26:
		return "电源(POWER)"
	case 24:
		return "音量+"
	case 25:
		return "音量-"
	case 66:
		return "回车(ENTER)"
	case 82:
		return "菜单(MENU)"
	default:
		return fmt.Sprintf("keycode %d", code)
	}
}
