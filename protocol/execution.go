package protocol

import (
	"autogov1/v2/core/gesture"
	"fmt"
	"math"
	"sort"
	"strings"
)

const CommandJournalV1 = "command_journal_v1"
const CommandFenceV1 = "command_fence_v1"
const RegistrationAckV1 = "registration_ack_v1"
const KernelIdentityV1 = "kernel_identity_v1"

func ValidateRegistration(m AgentMsg) error {
	if WantsRegistrationAck(m) && m.SessionID == "" {
		return fmt.Errorf("注册确认协议需要连接代次")
	}
	if m.DeviceID == "" || len(m.DeviceID) > 64 || strings.ContainsAny(m.DeviceID, "\x00\r\n\t") || len(m.SessionID) > 128 || len(m.Features) > 32 || len(m.Commands) > 256 {
		return fmt.Errorf("设备注册元数据无效")
	}
	if m.Kernel != nil {
		if len(m.Kernel.Version) > 96 || len(m.Kernel.SHA256) != 64 || m.Kernel.Size < 1 || m.Kernel.Size > 256<<20 {
			return fmt.Errorf("内核运行身份无效")
		}
		for _, c := range m.Kernel.SHA256 {
			if !strings.ContainsRune("0123456789abcdef", c) {
				return fmt.Errorf("内核摘要无效")
			}
		}
	}
	hasJournal, hasFence := false, false
	for _, feature := range m.Features {
		if feature == CommandJournalV1 {
			hasJournal = true
		}
		if feature == CommandFenceV1 {
			hasFence = true
		}
		if feature == CommandJournalV1 && (m.SessionID == "" || len(m.Commands) == 0) {
			return fmt.Errorf("执行日志协议需要连接代次和能力清单")
		}
	}
	if hasFence && !hasJournal {
		return fmt.Errorf("写入栅栏协议依赖持久执行日志")
	}
	return nil
}

// CommandReceipt 区分尚未开始、正在执行、已返回及效果不确定；返回不等于业务验收。
type CommandReceipt struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	OK        bool   `json:"ok"`
	Msg       string `json:"msg,omitempty"`
	Data      any    `json:"data,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
}

// NormalizeCommands 用于能力协商；新端只公布实际注册的命令。
func NormalizeCommands(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if len(out) == 256 {
			break
		}
		if value == "" || len(value) > 64 || strings.ContainsAny(value, "\x00\r\n\t ") || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// IsInputFrame 标记实时输入帧:按下/移动/抬起是同一个手势的三段,不是三个动作。
//
// 【为什么必须单独一类】。手势帧没有"请求标识"可说 —— 它不是一次可重放的业务动作,
// 而是"手现在在哪"的连续采样。若按动作处理(要求标识、写回执、进动作队列),帧与帧
// 之间会互相排队,抬手指那一帧一旦被挤掉,设备端就会一直按着,表现为长按/拖拽。
// 服务端与设备端必须共用这一个判定,两边的理解不一致就会各拒一半。
func IsInputFrame(c Cmd) bool {
	return c.Cmd == "touch"
}

// IsControlSignal 只允许传输和观察维护信令绕过动作执行队列。
func IsControlSignal(command string) bool {
	switch command {
	case "video_start", "video_stop", "video_key", "webrtc_answer", "webrtc_ice", "server_endpoints", "screenshot", "snap":
		return true
	}
	return false
}

// IsObservation 的命令只读取现场，允许与动作队列并行；带点击标记的识别不在此列。
func IsObservation(c Cmd) bool {
	switch c.Cmd {
	case "screen_state", "ui_dump", "observe", "ocr", "yolo", "cmp_color", "pixel", "color_count", "app_list", "app_info", "current_app", "device_info", "notifications", "proc_info", "cpu_usage", "kernel_info", "file_list", "file_info", "file_read", "view_file", "kv_get", "kv_list", "get_clipboard", "get_ime_list", "ui_wait", "input_info":
		return true
	case "findtap", "find_text", "find_color", "find_multi_colors":
		return !c.Tap
	}
	return false
}

// ValidateCommand 在云端和设备端共用，拒绝会造成越界或无界执行的参数。
func ValidateCommand(c Cmd) error {
	if len(c.SDP) > 128<<10 || len(c.ICE) > 32<<10 {
		return fmt.Errorf("媒体协商内容超过限制")
	}
	if c.Fence < 0 {
		return fmt.Errorf("设备写入栅栏无效")
	}
	if c.Cmd == "" || len(c.Cmd) > 64 || len(c.ID) > 128 || len(c.RequestID) > 128 || len(c.SessionID) > 128 {
		return fmt.Errorf("命令标识无效")
	}
	for _, v := range []float64{c.X, c.Y, c.X2, c.Y2, c.Thresh, c.Sim} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("坐标或阈值不是有限数值")
		}
	}
	coordinate := func(v float64) bool { return v >= 0 && v <= 1 }
	switch c.Cmd {
	case "tap", "touch", "swipe", "long_press", "pinch":
		if !coordinate(c.X) || !coordinate(c.Y) || !coordinate(c.X2) || !coordinate(c.Y2) {
			return fmt.Errorf("归一化坐标必须在 0 到 1 之间")
		}
		if c.Dur < 0 || c.Dur > 120000 {
			return fmt.Errorf("动作时长超过限制")
		}
	}
	if c.Cmd == "touch" && (c.Action < 0 || c.Action > 2) {
		return fmt.Errorf("触摸阶段无效")
	}
	if c.Cmd == "gesture" {
		if err := gesture.ValidateFrames(c.Frames); err != nil {
			return err
		}
	} else if c.Cmd == "touch" && len(c.Frames) > 0 {
		if len(c.Frames) != 1 || c.Frames[0].AtMS != 0 {
			return fmt.Errorf("实时触摸只接受一个立即提交的状态帧")
		}
		if err := gesture.ValidateContacts(c.Frames[0].Contacts); err != nil {
			return err
		}
		if (len(c.Frames[0].Contacts) == 0) != (c.Action == 1) {
			return fmt.Errorf("实时触摸的抬起标记与触点状态不一致")
		}
	} else if len(c.Frames) > 0 {
		return fmt.Errorf("该命令不接受轨迹参数")
	}

	if len(c.Text) > 1<<20 || len(c.Script) > 1<<20 || len(c.Tpl) > 4<<20 || len(c.Path) > 4096 {
		return fmt.Errorf("命令内容超过限制")
	}
	if c.Cmd == "command_cancel" || c.Cmd == "command_result" {
		if c.RequestID == "" || c.ID == "" || c.RequestID == c.ID {
			return fmt.Errorf("原请求标识无效")
		}
	}
	return nil
}

// WantsRegistrationAck 只有声明新握手协议的设备才等待显式注册确认。
func WantsRegistrationAck(m AgentMsg) bool {
	for _, feature := range m.Features {
		if feature == RegistrationAckV1 {
			return true
		}
	}
	return false
}

type RegistrationAck struct {
	ServerTimeMS int64  `json:"serverTimeMs,omitempty"` // 服务端生成确认时的 Unix 毫秒；供手机用单调时钟核对命令期限。
	Type         string `json:"type"`
	DeviceID     string `json:"deviceId"`
	SessionID    string `json:"sessionId"`
}
type KernelIdentity struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	// Arch 是内核包的架构后缀,取 Go 的 GOARCH 值(arm64 / amd64)。
	//
	// 【为什么由设备自己报】服务端要按架构挑对应的内核包,从型号或分辨率推断都不可靠
	// (同一型号可能不同 ABI)。老内核不带这个字段,服务端会退化成"只配了一种架构就按它算",
	// 这样新字段不会变成升级门禁(见 protocol.IsAgentMaintenance 的同类教训)。
	Arch string `json:"arch,omitempty"`
}
