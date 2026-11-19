package proxy

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// 协议族取值；空值按默认归一，兼容历史数据与旧请求体。
const (
	LocalFamilyIPv4 = "ipv4" // 本地监听 IPv4（默认，历史行为）
	LocalFamilyIPv6 = "ipv6" // 本地仅监听 IPv6
	LocalFamilyDual = "dual" // 本地 IPv6 双栈，同时接受 IPv4 连接

	RemoteFamilyAuto = "auto" // 远端自动解析（默认，历史行为）
	RemoteFamilyIPv4 = "ipv4" // 远端强制按 IPv4 解析（仅对主机名生效）
	RemoteFamilyIPv6 = "ipv6" // 远端强制按 IPv6 解析（仅对主机名生效）
)

// remoteHostRe 限定远端地址字符集（IPv4/IPv6/主机名），防御性校验；
// 参数以数组传递给 exec、不经 shell，此校验兼作输入错误的前置提示。
var remoteHostRe = regexp.MustCompile(`^[0-9A-Za-z._:\[\]-]+$`)

// hostKind 远端地址类型。
type hostKind string

const (
	hostIPv4 hostKind = "ipv4"
	hostIPv6 hostKind = "ipv6"
	hostName hostKind = "name"
)

// classifyHost 识别远端地址类型；仅对字面量 IP 判族，主机名交给 socat 解析。
// 输入须已经 NormalizeRemoteHost 规范化（IPv6 字面量不含方括号）。
func classifyHost(host string) hostKind {
	if strings.Contains(host, ":") {
		return hostIPv6
	}
	if ip := net.ParseIP(host); ip != nil {
		return hostIPv4
	}
	return hostName
}

// NormalizeLocalFamily 空值归一为默认 IPv4。
func NormalizeLocalFamily(f string) string {
	if f == "" {
		return LocalFamilyIPv4
	}
	return f
}

// NormalizeRemoteFamily 空值归一为自动解析。
func NormalizeRemoteFamily(f string) string {
	if f == "" {
		return RemoteFamilyAuto
	}
	return f
}

// NormalizeRemoteHost 校验并规范化远端地址：IPv6 字面量统一去方括号存储，
// 避免括号有无导致同一地址在库中呈现两种形态；含冒号但不是合法 IPv6 的输入报错。
func NormalizeRemoteHost(host string) (string, error) {
	if host == "" {
		return "", fmt.Errorf("远端地址不能为空")
	}
	if !remoteHostRe.MatchString(host) {
		return "", fmt.Errorf("远端地址含非法字符: %q", host)
	}
	if strings.Contains(host, ":") {
		trimmed := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
		if net.ParseIP(trimmed) == nil {
			return "", fmt.Errorf("非法的 IPv6 地址: %q", host)
		}
		return trimmed, nil
	}
	return host, nil
}

// ValidateFamilies 校验协议族取值；IP 字面量与显式远端族冲突时报错。
func ValidateFamilies(localFamily, remoteFamily, remoteHost string) error {
	switch NormalizeLocalFamily(localFamily) {
	case LocalFamilyIPv4, LocalFamilyIPv6, LocalFamilyDual:
	default:
		return fmt.Errorf("本地监听协议族取值非法: %q", localFamily)
	}
	rf := NormalizeRemoteFamily(remoteFamily)
	switch rf {
	case RemoteFamilyAuto, RemoteFamilyIPv4, RemoteFamilyIPv6:
	default:
		return fmt.Errorf("远端协议族取值非法: %q", remoteFamily)
	}
	switch classifyHost(remoteHost) {
	case hostIPv4:
		if rf == RemoteFamilyIPv6 {
			return fmt.Errorf("远端 %q 是 IPv4 地址，与强制 IPv6 解析冲突", remoteHost)
		}
	case hostIPv6:
		if rf == RemoteFamilyIPv4 {
			return fmt.Errorf("远端 %q 是 IPv6 地址，与强制 IPv4 解析冲突", remoteHost)
		}
	}
	return nil
}

// FormatRemoteHost 展示用远端地址：IPv6 字面量加方括号，避免与端口分隔混淆。
func FormatRemoteHost(host string, port int) string {
	if classifyHost(host) == hostIPv6 {
		return fmt.Sprintf("[%s]:%d", host, port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// listenAddress 构造本地监听 address：ipv6only 显式声明，
// 不受内核 net.ipv6.bindv6only 默认值影响。
func listenAddress(localPort int, localFamily string) (string, error) {
	switch NormalizeLocalFamily(localFamily) {
	case LocalFamilyIPv4:
		return fmt.Sprintf("TCP4-LISTEN:%d,bind=0.0.0.0,fork,reuseaddr", localPort), nil
	case LocalFamilyIPv6:
		return fmt.Sprintf("TCP6-LISTEN:%d,bind=[::],fork,reuseaddr,ipv6only=1", localPort), nil
	case LocalFamilyDual:
		return fmt.Sprintf("TCP6-LISTEN:%d,bind=[::],fork,reuseaddr,ipv6only=0", localPort), nil
	default:
		return "", fmt.Errorf("本地监听协议族取值非法: %q", localFamily)
	}
}

// remoteAddress 构造远端 address：字面量按自身族（IPv6 加方括号），
// 主机名按远端族选择解析约束，auto 保持 socat 默认解析。
func remoteAddress(remoteHost string, remotePort int, remoteFamily string) (string, error) {
	prefix := "TCP"
	switch {
	case classifyHost(remoteHost) == hostIPv6:
		remoteHost = "[" + remoteHost + "]"
		prefix = "TCP6"
	case classifyHost(remoteHost) == hostIPv4:
		prefix = "TCP4"
	case NormalizeRemoteFamily(remoteFamily) == RemoteFamilyIPv4:
		prefix = "TCP4"
	case NormalizeRemoteFamily(remoteFamily) == RemoteFamilyIPv6:
		prefix = "TCP6"
	}
	return fmt.Sprintf("%s:%s:%d", prefix, remoteHost, remotePort), nil
}

// SocatArgs 构造 socat 端口映射参数（fork 模式）：
// 本地与远端协议族独立选择，可组合出 v4→v6、v6→v4 的转换转发。
func SocatArgs(localPort int, localFamily string, remoteHost string, remotePort int, remoteFamily string) ([]string, error) {
	listen, err := listenAddress(localPort, localFamily)
	if err != nil {
		return nil, err
	}
	remote, err := remoteAddress(remoteHost, remotePort, remoteFamily)
	if err != nil {
		return nil, err
	}
	return []string{listen, remote}, nil
}
