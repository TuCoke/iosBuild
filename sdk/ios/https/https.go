// Package https 对齐 AutoGo iOS 的网络 API（3 个函数，同名同参同返回）。
//
// 用 Go 的 net/http 直连：iOS 上 Go 自带的解析器不依赖 NSURLSession，
// 也就不会踩 ATS（明文 HTTP 默认被拦）——我们自己控制是否允许 http。
// 与 AutoGo 一致：返回 (状态码, 响应体)，失败时状态码 0、体为 nil。
package https

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

func client(timeout int) *http.Client {
	if timeout <= 0 {
		timeout = 30
	}
	return &http.Client{Timeout: time.Duration(timeout) * time.Second}
}

// Get 发 GET 请求。timeout 单位是秒，<=0 用 30 秒。
func Get(url string, timeout int) (code int, data []byte) {
	resp, err := client(timeout).Get(url)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, body
}

// Post 发 POST；headers 可为 nil。data 为 nil 时不带请求体。
func Post(url string, data []byte, headers map[string]string, timeout int) (code int, body []byte) {
	var reader io.Reader
	if data != nil {
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(http.MethodPost, url, reader)
	if err != nil {
		return 0, nil
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client(timeout).Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, out
}

// PostMultipart 以 multipart/form-data 上传一个文件，字段名固定 file（与 AutoGo 一致）。
func PostMultipart(url, fileName string, fileData []byte, timeout int) (code int, data []byte) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", fileName)
	if err != nil {
		return 0, nil
	}
	if _, err = part.Write(fileData); err != nil {
		return 0, nil
	}
	if err = w.Close(); err != nil {
		return 0, nil
	}
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		return 0, nil
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := client(timeout).Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, body
}
