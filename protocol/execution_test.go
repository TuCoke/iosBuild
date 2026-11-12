package protocol

import (
	"autogov1/v2/core/gesture"
	"testing"
)

// 升级必须经过正常写入准入，不能被分到输入帧、观察或控制信令快车道。
func TestSelfUpdateUsesWriteAdmission(t *testing.T) {
	cmd := Cmd{Cmd: "self_update"}
	if IsInputFrame(cmd) || IsObservation(cmd) || IsControlSignal(cmd.Cmd) {
		t.Fatal("self_update 不能绕过设备写入准入")
	}
}

func TestMultiTouchAdmission(t *testing.T) {
	c := Cmd{Cmd: "gesture", Frames: []gesture.Frame{{AtMS: 0, Contacts: []gesture.Contact{{ID: 0, X: .2, Y: .3}, {ID: 1, X: .7, Y: .5}}}}}
	if ValidateCommand(c) != nil || IsObservation(c) || IsInputFrame(c) || IsControlSignal(c.Cmd) || !RequiresActionAcknowledgment(c.Cmd) {
		t.Fatal("轨迹必须经过普通写入准入")
	}
	c.Cmd = "touch"
	c.Action = 0
	if ValidateCommand(c) != nil || !IsInputFrame(c) {
		t.Fatal("实时多指帧未被接受")
	}
	c.Action = 1
	if ValidateCommand(c) == nil {
		t.Fatal("有活动触点不能声明全部抬起")
	}
	c.Frames[0].Contacts = nil
	if ValidateCommand(c) != nil {
		t.Fatal("空快照应允许抬起")
	}
	c.Cmd = "tap"
	if ValidateCommand(c) == nil {
		t.Fatal("非轨迹命令不得夹带frames")
	}
	if !IsObservation(Cmd{Cmd: "input_info"}) || IsObservation(Cmd{Cmd: "input_reset"}) {
		t.Fatal("诊断与重置的读写分类错误")
	}
}
