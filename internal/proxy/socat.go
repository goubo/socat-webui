package proxy

import (
	"fmt"
	"regexp"
)

// remoteHostRe 限定远端地址字符集（IPv4/IPv6/主机名），防御性校验；
// 参数以数组传递给 exec、不经 shell，此校验兼作输入错误的前置提示。
var remoteHostRe = regexp.MustCompile(`^[0-9A-Za-z._:\[\]-]+$`)

// ValidateRemoteHost 校验远端地址非空且仅含允许字符。
func ValidateRemoteHost(host string) error {
	if host == "" {
		return fmt.Errorf("远端地址不能为空")
	}
	if !remoteHostRe.MatchString(host) {
		return fmt.Errorf("远端地址含非法字符: %q", host)
	}
	return nil
}

// SocatArgs 构造 socat 端口映射参数：TCP-LISTEN(本地,fork) → TCP(远端)。
func SocatArgs(localPort int, remoteHost string, remotePort int) []string {
	return []string{
		fmt.Sprintf("TCP-LISTEN:%d,bind=0.0.0.0,fork,reuseaddr", localPort),
		fmt.Sprintf("TCP:%s:%d", remoteHost, remotePort),
	}
}
