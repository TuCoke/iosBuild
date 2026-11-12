package update

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHealthProbationAndPersistentFailureRollback(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	state := ReleaseState{Version: 1, Current: a, Pending: b, Attempt: b}
	for i := 0; i < 2; i++ {
		state = applyHealth(state, HealthObservation{Digest: b, Ready: true}, time.Now())
		if state.Current != a {
			t.Fatal("候选过早确认")
		}
	}
	state = applyHealth(state, HealthObservation{Digest: b, Ready: true}, time.Now())
	if state.Current != b || state.Previous != a || state.Attempt != "" {
		t.Fatal("候选确认状态错误", state)
	}
	for i := 0; i < 3; i++ {
		state = applyHealth(state, HealthObservation{Digest: b}, time.Now())
	}
	if state.Current != a || state.Rejected != b || !state.NeedsRestart {
		t.Fatal("已确认版本持续失败没有安排回退", state)
	}
	for i := 0; i < 3; i++ {
		state = applyHealth(state, HealthObservation{Digest: a, Ready: true}, time.Now())
	}
	if state.NeedsRestart || state.Rejected != b {
		t.Fatal("回退版本确认后未恢复可用状态，或丢失坏版本隔离", state)
	}
}

func TestHealthTimeoutAndOldObservationKeepNewPending(t *testing.T) {
	a, b, c := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	state := ReleaseState{Version: 1, Current: b, Previous: a, Pending: c}
	state = applyHealth(state, HealthObservation{Digest: b, TimedOut: true}, time.Now())
	if state.Current != a || state.Pending != c || state.Rejected != b {
		t.Fatal("旧 SDK 健康观测覆盖了新安装候选", state)
	}
	dir := t.TempDir()
	if err := writeState(dir, state); err != nil {
		t.Fatal(err)
	}
	read, err := readState(dir)
	if err != nil || read.Rejected != b || !read.NeedsRestart {
		t.Fatal("健康状态未持久化", read, err)
	}
	var health HealthState
	if json.Unmarshal(read.Health, &health) != nil || health.Failures != 1 {
		t.Fatal("健康计数丢失")
	}
}

func TestDocumentsHealthProbeCleansUpAndReportsErrors(t *testing.T) {
	dir := t.TempDir()
	if err := probeDocuments(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatal("健康探测遗留临时文件")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if probeDocuments(ctx, dir) == nil || probeDocuments(context.Background(), "relative") == nil {
		t.Fatal("探测错误被忽略")
	}
}
