package runtime

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

// ValidateConfig 禁止把凭据放进 URL，公网默认必须使用 WSS 和服务端身份凭据。
func ValidateConfig(c Config) error {
	u, e := url.Parse(c.Server)
	if e != nil || u.Host == "" || (u.Scheme != "ws" && u.Scheme != "wss") || u.Path != "/agent" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("Agent 地址必须是 ws(s)://主机/agent，不能带查询参数或凭据")
	}
	if strings.ContainsAny(c.Token, "\x00\r\n") || len(c.Token) > 8192 {
		return fmt.Errorf("Agent 凭据格式无效")
	}
	ip := net.ParseIP(u.Hostname())
	local := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if !local && u.Scheme != "wss" && !c.AllowInsecure {
		return fmt.Errorf("公网 Agent 默认要求 WSS；内网调试须显式允许明文连接")
	}
	if !local && strings.TrimSpace(c.Token) == "" {
		return fmt.Errorf("非回环服务端必须配置独立 Agent 凭据")
	}
	if c.DataDir == "" || !filepath.IsAbs(c.DataDir) {
		return fmt.Errorf("必须指定手机上的绝对私有数据目录")
	}
	return nil
}
