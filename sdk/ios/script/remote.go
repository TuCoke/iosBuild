// 热更新入口：从远端拉脚本、校验、缓存、执行。
//
// 这里的关键不是"下载"，而是"下载失败时不能把线上跑挂"：
//
//	拉取失败  → 用本地最后一份能跑的（cache/<name>.go）
//	哈希不符  → 同样回退，并把原因报上去（可能是 CDN 半包，也可能是被改过）
//	本地也没有 → 才真的失败
//
// 只有这样才能做到"脚本随时可改"，而不是"改一次能不能跑看运气"。
package script

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"

	"autogov1/v2/sdk/ios/files"
	"autogov1/v2/sdk/ios/https"
)

// RemoteOptions 控制一次远端脚本执行。
type RemoteOptions struct {
	// URL 是脚本地址（http/https 均可，内网 IP 也行）。
	URL string
	// SHA256 是期望的脚本哈希（十六进制，可空：空则跳过校验）。
	SHA256 string
	// CacheName 是本地缓存文件名（例如 "daily.go"）。
	CacheName string
	// Timeout 是下载超时。
	Timeout time.Duration
}

// cacheDir 是脚本缓存目录（在 App 沙盒里）。
const cacheDir = "Scripts"

func (r *Runner) cachePath(name string) string {
	if name == "" {
		name = "default"
	}
	return files.Path(filepath.Join(cacheDir, name+".go"))
}

// RunRemote 拉取并执行脚本；失败时回退到本地缓存。
//
// 返回的 Result 里 Logs 会带上"用了哪个版本、为什么回退"，
// 排查线上问题时这行日志比什么都重要。
func (r *Runner) RunRemote(ctx context.Context, opts RemoteOptions) (Result, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 20 * time.Second
	}
	if opts.URL == "" {
		return r.RunCache(ctx, opts.CacheName)
	}

	downloadCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	status, body := https.Get(opts.URL, int(opts.Timeout/time.Millisecond))
	_ = downloadCtx // 下载由 https 包内的 client 超时控制；这里保留 ctx 供后续加钩子
	if status != 200 || len(body) == 0 {
		res, err := r.RunCache(ctx, opts.CacheName)
		res.Logs = append([]string{fmt.Sprintf("脚本拉取失败(HTTP %d)，回退本地缓存", status)}, res.Logs...)
		return res, err
	}

	if opts.SHA256 != "" {
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != opts.SHA256 {
			res, err := r.RunCache(ctx, opts.CacheName)
			res.Logs = append([]string{fmt.Sprintf("脚本哈希不符(期望 %s，实际 %s)，回退本地缓存", opts.SHA256[:8], got[:8])}, res.Logs...)
			return res, err
		}
	}

	// 先落盘再执行：这样即使本次执行崩了，下次也有可回退的版本。
	// files.Write 不返回错误（读不到就给零值那套约定），落盘失败不影响本次执行，
	// 只是下次没有可回退的版本 —— 那属于"下次再说"的问题，不在这里吞掉本次结果。
	files.Write(r.cachePath(opts.CacheName), string(body))

	res, err := r.Run(ctx, string(body))
	res.Logs = append([]string{fmt.Sprintf("脚本来自远端(%d 字节)", len(body))}, res.Logs...)
	return res, err
}

// RunCache 执行本地缓存的脚本（没有就报错）。
func (r *Runner) RunCache(ctx context.Context, cacheName string) (Result, error) {
	path := r.cachePath(cacheName)
	source := files.Read(path)
	if source == "" {
		return Result{}, fmt.Errorf("没有可用的脚本：远端拉取失败，本地也没有缓存 %s", path)
	}
	res, err := r.Run(ctx, source)
	res.Logs = append([]string{"脚本来自本地缓存"}, res.Logs...)
	return res, err
}

// CacheSource 把一段脚本写进缓存（供"先审后发"的场景预置）。
func CacheSource(cacheName, source string) {
	files.Write(filepath.Join(cacheDir, cacheName+".go"), source)
}

// CachedSource 读回缓存内容（不存在返回空串）。
func CachedSource(cacheName string) string {
	return files.Read(filepath.Join(cacheDir, cacheName+".go"))
}
