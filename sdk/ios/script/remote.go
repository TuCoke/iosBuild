// 远端脚本是本项目的更新扩展；仅成功执行的版本才能替换本地缓存。
package script

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"autogov1/v2/sdk/ios/files"
)

type RemoteOptions struct {
	URL       string
	SHA256    string
	CacheName string
	Timeout   time.Duration
}

const cacheDir = "Scripts"
const maxRemoteBytes = 4 << 20

// cacheFilename 不接受路径，调用者无法通过缓存名覆盖工程或 SDK。
func cacheFilename(name string) (string, error) {
	if name == "" {
		name = "default"
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\:\x00") {
		return "", fmt.Errorf("缓存名必须是单个文件名")
	}
	name = strings.TrimSuffix(name, ".go")
	if name == "" {
		return "", fmt.Errorf("缓存名不能为空")
	}
	return files.Path(filepath.Join(cacheDir, name+".go")), nil
}
func (r *Runner) cachePath(name string) string { p, _ := cacheFilename(name); return p }

func (r *Runner) RunRemote(ctx context.Context, opts RemoteOptions) (Result, error) {
	return r.RunRemoteWithExecutor(ctx, opts, r.Run)
}

// RunRemoteWithExecutor 使远端下载/缓存与宿主的工作进程隔离共用同一执行入口。
func (r *Runner) RunRemoteWithExecutor(ctx context.Context, opts RemoteOptions, execute func(context.Context, string) (Result, error)) (Result, error) {
	if execute == nil {
		return Result{}, fmt.Errorf("缺少脚本执行器")
	}

	if ctx == nil {
		return Result{}, fmt.Errorf("脚本 context 不能为空")
	}
	filename, err := cacheFilename(opts.CacheName)
	if err != nil {
		return Result{}, err
	}
	if opts.URL == "" {
		return r.runCacheUsing(ctx, opts.CacheName, execute)
	}
	expected := strings.ToLower(opts.SHA256)
	if expected != "" {
		digest, e := hex.DecodeString(expected)
		if e != nil || len(digest) != sha256.Size {
			return Result{}, fmt.Errorf("SHA256 必须为 64 位十六进制字符串")
		}
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 20 * time.Second
	}
	downloadCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, opts.URL, nil)
	if err != nil {
		return Result{}, err
	}
	fallback := func(reason string) (Result, error) {
		if e := ctx.Err(); e != nil {
			return Result{}, e
		}
		res, e := r.runCacheUsing(ctx, opts.CacheName, execute)
		res.Logs = append([]string{reason + "，回退本地缓存"}, res.Logs...)
		return res, e
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return fallback("脚本下载失败: " + err.Error())
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fallback(fmt.Sprintf("脚本下载失败(HTTP %d)", response.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxRemoteBytes+1))
	if err != nil {
		return fallback("脚本下载中断: " + err.Error())
	}
	if len(body) == 0 || len(body) > maxRemoteBytes {
		return fallback("脚本为空或超过 4 MiB")
	}
	sum := sha256.Sum256(body)
	actual := hex.EncodeToString(sum[:])
	if expected != "" && expected != actual {
		return fallback("脚本哈希不符")
	}
	// 新脚本已经执行过时不能再自动重跑旧脚本，否则会重复触发设备操作。
	res, err := execute(ctx, string(body))
	res.Logs = append([]string{fmt.Sprintf("脚本来自远端(%d 字节，SHA256=%s)", len(body), actual)}, res.Logs...)
	if err != nil {
		return res, err
	}
	if err = atomicCache(filename, body); err != nil {
		res.Logs = append(res.Logs, "脚本已执行，但缓存保存失败: "+err.Error())
	}
	return res, nil
}

func atomicCache(filename string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(filename), ".script-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(body); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), filename)
}
func (r *Runner) RunCache(ctx context.Context, name string) (Result, error) {
	return r.runCacheUsing(ctx, name, r.Run)
}
func (r *Runner) runCacheUsing(ctx context.Context, name string, execute func(context.Context, string) (Result, error)) (Result, error) {
	filename, err := cacheFilename(name)
	if err != nil {
		return Result{}, err
	}
	body, err := readLimitedFile(filename, maxRemoteBytes)
	if err != nil {
		return Result{}, fmt.Errorf("读取缓存脚本失败: %w", err)
	}
	res, err := execute(ctx, string(body))
	res.Logs = append([]string{"脚本来自本地缓存"}, res.Logs...)
	return res, err
}
func CacheSource(name, source string) {
	if filename, err := cacheFilename(name); err == nil {
		_ = atomicCache(filename, []byte(source))
	}
}
func CachedSource(name string) string {
	filename, err := cacheFilename(name)
	if err != nil {
		return ""
	}
	body, err := readLimitedFile(filename, maxRemoteBytes)
	if err != nil {
		return ""
	}
	return string(body)
}
