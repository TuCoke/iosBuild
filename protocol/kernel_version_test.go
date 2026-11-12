package protocol

import "testing"

// 只编写断言，本批不运行。
func TestKernelVersionOrdering(t *testing.T) {
	cases := [][2]string{{"1.10.0", "1.9.0"}, {"v2.0.0", "v2.0.0-rc.1"}, {"202610290001", "202610280001"}, {"20261029000100", "20261028"}}
	for _, pair := range cases {
		order, err := CompareKernelVersions(pair[0], pair[1])
		if err != nil || order <= 0 {
			t.Fatalf("版本顺序错误 %v: %d %v", pair, order, err)
		}
	}
	if _, err := CompareKernelVersions("20261029", "v1.0.0"); err == nil {
		t.Fatal("混用版本体系却静默排序")
	}
	if _, err := CompareKernelVersions("../latest", "v1.0.0"); err == nil {
		t.Fatal("无效版本被接受")
	}
}
func TestRegistrationAcknowledgmentRequiresSession(t *testing.T) {
	if err := ValidateRegistration(AgentMsg{DeviceID: "phone", Features: []string{RegistrationAckV1}}); err == nil {
		t.Fatal("没有连接代次却协商注册确认")
	}
}
