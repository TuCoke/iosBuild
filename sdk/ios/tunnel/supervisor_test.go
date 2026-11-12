package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 替代进程边界，验证状态和启动次数；不把替代进程当成真实 iOS/frpc 验收。
func TestRepeatedStartDoesNotOverwritePIDAndStopCancelsSupervisor(t *testing.T) {
	root := t.TempDir()
	mu.Lock()
	oldCall, oldPID, oldBinary := callNative, activePID, activeBinary
	directoryMu.Lock()
	oldRoot := documentRoot
	documentRoot = root
	directoryMu.Unlock()
	stopSupervisorLocked()
	var alive atomic.Bool
	alive.Store(true)
	var spawns atomic.Int32
	callNative = func(_ context.Context, op string, _ any, out any) error {
		var value any
		switch op {
		case "app.bundle_dir":
			value = map[string]any{"path": root}
		case "utils.alive":
			value = map[string]any{"alive": alive.Load()}
		case "utils.wait":
			value = map[string]any{"finished": !alive.Load(), "code": 0}
		case "utils.kill":
			alive.Store(false)
			value = map[string]any{"ok": true}
		case "utils.spawn":
			spawns.Add(1)
			return fmt.Errorf("模拟恢复启动失败")
		default:
			return fmt.Errorf("未预期调用 %s", op)
		}
		if out == nil {
			return nil
		}
		raw, _ := json.Marshal(value)
		return json.Unmarshal(raw, out)
	}
	activePID, activeBinary = 12345, filepath.Join(root, "frpc-ios")
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		stopSupervisorLocked()
		callNative, activePID, activeBinary = oldCall, oldPID, oldBinary
		restartAttempts, restartError, lastStartError = 0, "", ""
		directoryMu.Lock()
		documentRoot = oldRoot
		directoryMu.Unlock()
		mu.Unlock()
	})
	if err := os.MkdirAll(filepath.Join(root, "Tunnel"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Server: "example.com", LocalPort: 18990, RemotePort: 18990}
	if err := os.WriteFile(filepath.Join(root, configRel), []byte(Render(cfg)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "frpc-ios"), []byte("替代文件"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		st, err := Start(context.Background(), cfg)
		if err != nil || st.PID != 12345 || !st.Supervising {
			t.Fatalf("重复启动不幂等: %+v %v", st, err)
		}
	}
	changed := cfg
	changed.RemotePort++
	if _, err := Start(context.Background(), changed); err == nil {
		t.Fatal("运行中配置变化没有拒绝")
	}
	if spawns.Load() != 0 {
		t.Fatal("重复启动产生了额外进程")
	}
	// 原进程退出后才允许看护发起一次恢复；失败后继续保留可查询的诊断。
	alive.Store(false)
	deadline := time.Now().Add(5 * time.Second)
	for spawns.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if spawns.Load() != 1 {
		t.Fatal("进程退出后没有进行有界恢复", spawns.Load())
	}
	if _, err := Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if Query(context.Background()).Supervising {
		t.Fatal("显式停止后仍在看护")
	}
	time.Sleep(2100 * time.Millisecond)
	if spawns.Load() != 1 {
		t.Fatal("显式停止后被自动重启")
	}
}

func TestRenderRetriesInitialLogin(t *testing.T) {
	text := Render(Config{Server: "example.com", LocalPort: 18990})
	if !strings.Contains(text, "loginFailExit = false\n") {
		t.Fatal("首次网络不可用仍会直接退出")
	}
}
