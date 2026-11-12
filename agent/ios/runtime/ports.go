// Package runtime 是独立的 iOS Agent 运行时，不导入安卓 Agent 核心或安卓 SDK。
package runtime

import (
	"autogov1/v2/protocol"
	"context"
)

// Info 仅用于注册和心跳；平台实现负责提供实际设备信息。
type Info struct {
	OS         string  `json:"os"`
	OSVersion  string  `json:"osVersion"`
	Model      string  `json:"model"`
	Name       string  `json:"name"`
	Identifier string  `json:"identifier"`
	Battery    float64 `json:"battery"`
	Display    struct {
		Width    int `json:"width"`
		Height   int `json:"height"`
		Rotation int `json:"rotation"`
	} `json:"display"`
}
type VideoOptions struct{ Width, FPS, Bitrate int }
type VideoPacket struct {
	Data          []byte
	PTS           int64
	Keyframe      bool
	Width, Height int
}
type VideoSource interface {
	Read(context.Context) (VideoPacket, error)
	RequestKeyFrame(context.Context) error
	SetBitrate(context.Context, int) error
	Close() error
}

// Device 只由 iOS 驱动实现；运行时不根据命令猜测平台，也不回退 ADB。
type Device interface {
	Info(context.Context) (Info, error)
	Healthy(context.Context) error
	Commands() []string
	Execute(context.Context, protocol.Cmd) (any, error)
	Tree(context.Context) ([]map[string]any, error)
	Screenshot(context.Context) ([]byte, error)
	OpenVideo(context.Context, VideoOptions) (VideoSource, error)
	Unknown(error) bool
}
