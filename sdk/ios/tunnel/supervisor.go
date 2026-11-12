package tunnel

import (
	"context"
	"fmt"
	"os"
	"time"
)

// 只看护当前明确要求运行的隧道；显式停止必须撤销重启需求。
var supervisorCancel context.CancelFunc
var supervisorID uint64
var supervising bool
var restartAttempts int
var restartError string

func Start(ctx context.Context, cfg Config) (Status, error) {
	mu.Lock()
	defer mu.Unlock()
	if ctx == nil || cfg.Server == "" || cfg.LocalPort <= 0 {
		return startLocked(ctx, cfg) // 保留既有参数错误与诊断回执。
	}
	if err := ctx.Err(); err != nil {
		return Status{StartError: err.Error()}, err
	}
	if err := ensureDirectory(ctx); err != nil {
		return Status{StartError: err.Error()}, err
	}
	if readPID() > 0 {
		status := queryLocked(ctx)
		if status.LastError != "" {
			return status, fmt.Errorf("尚未确认旧隧道状态: %s", status.LastError)
		}
		if status.Running {
			raw, err := os.ReadFile(tunnelPath(configRel))
			if err != nil || string(raw) != Render(cfg) {
				return status, fmt.Errorf("隧道正在运行，请先停止后再更换配置")
			}
			if !supervising {
				startSupervisorLocked(cfg)
			}
			status.Supervising = true
			return status, nil
		}
	}
	status, err := startLocked(ctx, cfg)
	if err == nil || status.Running {
		startSupervisorLocked(cfg)
	}
	status.Supervising = supervising
	return status, err
}

func stopSupervisorLocked() {
	supervisorID++
	if supervisorCancel != nil {
		supervisorCancel()
		supervisorCancel = nil
	}
	supervising = false
}
func startSupervisorLocked(cfg Config) {
	stopSupervisorLocked()
	ctx, cancel := context.WithCancel(context.Background())
	supervisorCancel, supervising = cancel, true
	restartAttempts, restartError = 0, ""
	id := supervisorID
	go supervise(ctx, id, cfg)
}

func supervise(ctx context.Context, id uint64, cfg Config) {
	delay := 2 * time.Second
	var stableSince time.Time
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		mu.Lock()
		if id != supervisorID || ctx.Err() != nil {
			mu.Unlock()
			return
		}
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		status := queryLocked(probe)
		cancel()
		if status.Running && status.LastError == "" {
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
			if time.Since(stableSince) >= time.Minute {
				restartAttempts, restartError = 0, ""
			}
			delay = 2 * time.Second
			mu.Unlock()
			continue
		}
		stableSince = time.Time{}
		if restartAttempts >= 5 {
			restartError = "隧道连续恢复失败，请检查日志后重新启动"
			stopSupervisorLocked()
			mu.Unlock()
			return
		}
		restartAttempts++
		if status.LastError != "" {
			// 无法确认旧进程退出时只报告，不能拉起第二份进程。
			restartError = status.LastError
		} else {
			restartCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, err := startLocked(restartCtx, cfg)
			cancel()
			if err != nil {
				restartError = err.Error()
			} else {
				restartError = ""
			}
		}
		delay = time.Second << min(restartAttempts, 4)
		mu.Unlock()
	}
}
