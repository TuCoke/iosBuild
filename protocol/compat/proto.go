// Package proto 保留旧导入路径，通信类型由公共 protocol 模块统一维护。
// 新模块直接使用 autogov1/protocol，避免再次复制协议定义。
package proto

import "autogov1/v2/protocol"

const (
	BinVideo      = protocol.BinVideo
	BinThumbnail  = protocol.BinThumbnail
	BinScreenshot = protocol.BinScreenshot
)

type AgentMsg = protocol.AgentMsg
type Cmd = protocol.Cmd
type ICEServer = protocol.ICEServer
type Intent = protocol.Intent
type ClientMsg = protocol.ClientMsg
type DeviceInfo = protocol.DeviceInfo
type LogEntry = protocol.LogEntry
type ServerMsg = protocol.ServerMsg

func KeyName(code int) string { return protocol.KeyName(code) }
