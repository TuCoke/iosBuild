package update

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"autogov1/v2/sdk/ios/device"
	"autogov1/v2/sdk/ios/internal/native"
)

type HealthReport struct {
	Ready                bool              `json:"ready"`
	Protocol             int               `json:"protocol"`
	Checks               map[string]string `json:"checks"`
	ElapsedMS            int64             `json:"elapsedMs"`
	CapabilityValidation string            `json:"capabilityValidation"`
}

type HealthObservation struct {
	Digest   string          `json:"digest"`
	Ready    bool            `json:"ready"`
	TimedOut bool            `json:"timedOut"`
	Report   json.RawMessage `json:"report"`
}
type HealthState struct {
	Digest    string          `json:"digest,omitempty"`
	Successes int             `json:"successes"`
	Failures  int             `json:"failures"`
	Ready     bool            `json:"ready"`
	CheckedAt string          `json:"checkedAt"`
	Report    json.RawMessage `json:"report,omitempty"`
}

// 健康状态也由 Go 安装锁保护，不能让 ObjC 定时器的旧状态覆盖刚安装的 pending。
func RecordHealth(ctx context.Context, observation HealthObservation) (ReleaseState, error) {
	if observation.Digest != "" && !validDigest(observation.Digest) {
		return ReleaseState{}, fmt.Errorf("健康观测摘要无效")
	}
	if observation.Digest != "" && snapshotCopy()["sha256"] != observation.Digest {
		return ReleaseState{}, fmt.Errorf("健康观测不属于实际加载的 SDK")
	}
	if len(observation.Report) > 64<<10 {
		return ReleaseState{}, fmt.Errorf("健康报告过大")
	}
	paths, err := device.Paths(ctx)
	if err != nil {
		return ReleaseState{}, err
	}
	installMu.Lock()
	defer installMu.Unlock()
	if err := ctx.Err(); err != nil {
		return ReleaseState{}, err
	}
	dir := filepath.Join(paths.Documents, "Plugins")
	state, err := readState(dir)
	if err != nil {
		return state, err
	}
	state = applyHealth(state, observation, time.Now())
	err = writeState(dir, state)
	return state, err
}

func applyHealth(state ReleaseState, observation HealthObservation, now time.Time) ReleaseState {
	var health HealthState
	_ = json.Unmarshal(state.Health, &health)
	if health.Digest != observation.Digest {
		health = HealthState{Digest: observation.Digest}
	}
	health.Ready, health.CheckedAt, health.Report = observation.Ready, now.UTC().Format(time.RFC3339Nano), observation.Report
	if observation.Ready {
		health.Successes++
		health.Failures = 0
		if health.Successes >= 3 && (observation.Digest == "" || state.Rejected != observation.Digest) {
			state.NeedsRestart = false
			state.LastError = ""
		}
		if observation.Digest != "" && state.Pending == observation.Digest && state.Attempt == observation.Digest && health.Successes >= 3 {
			if state.Current != "" && state.Current != observation.Digest {
				state.Previous = state.Current
			}
			state.Current = observation.Digest
			state.Pending = ""
			state.Attempt = ""
			state.NeedsRestart = false
			state.LastError = ""
			if state.Rejected == observation.Digest {
				state.Rejected = ""
			}
		}
	} else {
		health.Successes = 0
		health.Failures++
		if observation.TimedOut || health.Failures >= 3 {
			state.NeedsRestart = true
			state.LastError = "SDK 持续健康检查失败，需要重启以回退；不会在运行中卸载 Go 库"
			if observation.Digest != "" {
				state.Rejected = observation.Digest
				if state.Current == observation.Digest {
					state.Current = state.Previous
					state.Previous = ""
				}
				if state.Pending == observation.Digest {
					state.Pending = ""
				}
				if state.Attempt == observation.Digest {
					state.Attempt = ""
				}
			}
		}
	}
	state.Health, _ = json.Marshal(health)
	return state
}

// Health 只验证运行时、原生 ABI、实际加载映像和持久目录；不点击、不采集屏幕，
// 不把缺少系统授权的可选能力当成 SDK 损坏，也不与长脚本争用动作闸门。
func Health(ctx context.Context) HealthReport {
	started := time.Now()
	report := HealthReport{Protocol: 2, Checks: map[string]string{}, CapabilityValidation: "not-run"}
	paths, err := device.Paths(ctx)
	if err != nil {
		report.Checks["runtime-paths"] = err.Error()
		return report
	}
	var abi struct {
		Protocol        int  `json:"protocol"`
		InputLease      bool `json:"inputLease"`
		WorkerDocuments bool `json:"workerDocuments"`
	}
	if err := native.Call(ctx, "runtime.health", nil, &abi); err != nil {
		report.Checks["native-abi"] = err.Error()
	} else if abi.Protocol != 2 || !abi.InputLease || !abi.WorkerDocuments {
		report.Checks["native-abi"] = "宿主运行协议不兼容"
	} else {
		report.Checks["native-abi"] = "ok"
	}
	report.Checks["loaded-image"] = "ok"
	loaded := snapshotCopy()
	current := fileStatus(paths.LoadedImage)
	if current["sha256"] == nil || loaded["sha256"] == nil || current["sha256"] != loaded["sha256"] || loaded["path"] != paths.LoadedImage {
		report.Checks["loaded-image"] = "实际加载映像缺失或磁盘文件已变化，需要重新启动"
	}
	if err := probeDocuments(ctx, paths.Documents); err != nil {
		report.Checks["documents"] = err.Error()
	} else {
		report.Checks["documents"] = "ok"
	}
	report.Ready = true
	for _, value := range report.Checks {
		if value != "ok" {
			report.Ready = false
		}
	}
	report.ElapsedMS = time.Since(started).Milliseconds()
	return report
}

func probeDocuments(ctx context.Context, directory string) error {
	if ctx == nil {
		return fmt.Errorf("健康探测 context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(directory) {
		return fmt.Errorf("持久目录不是绝对路径")
	}
	f, err := os.CreateTemp(directory, ".agov-health-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write([]byte("AGOV-HEALTH-2"))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	if string(raw) != "AGOV-HEALTH-2" {
		return fmt.Errorf("持久目录读写核对失败")
	}
	if err := os.Remove(name); err != nil {
		return err
	}
	return ctx.Err()
}
