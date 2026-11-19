package proxy

import (
	"net"
	"strconv"
	"time"
)

const dialTimeout = 3 * time.Second

// PrecheckResult 前置检查结果：远端连通 + 本地端口空闲。
type PrecheckResult struct {
	RemoteOK  bool   `json:"remoteOk"`
	RemoteErr string `json:"remoteErr,omitempty"`
	LocalFree bool   `json:"localFree"`
	LocalErr  string `json:"localErr,omitempty"`
}

// remoteNetwork 远端探测网络：字面量按自身族，主机名按远端族（auto 不限制）。
func remoteNetwork(host, remoteFamily string) string {
	switch classifyHost(host) {
	case hostIPv4:
		return "tcp4"
	case hostIPv6:
		return "tcp6"
	}
	switch NormalizeRemoteFamily(remoteFamily) {
	case RemoteFamilyIPv4:
		return "tcp4"
	case RemoteFamilyIPv6:
		return "tcp6"
	}
	return "tcp"
}

// localNetwork 本地端口探测网络：dual 用双栈 tcp，与 socat ipv6only=0 行为一致。
func localNetwork(localFamily string) string {
	switch NormalizeLocalFamily(localFamily) {
	case LocalFamilyIPv6:
		return "tcp6"
	case LocalFamilyDual:
		return "tcp"
	default:
		return "tcp4"
	}
}

// CheckRemote 从本机 TCP dial 远端地址，代表目标服务可用性。
func CheckRemote(host string, port int, remoteFamily string) error {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout(remoteNetwork(host, remoteFamily), addr, dialTimeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

// CheckLocalPortFree 通过临时监听探测本地端口在指定协议族上是否空闲。
func CheckLocalPortFree(port int, localFamily string) error {
	ln, err := net.Listen(localNetwork(localFamily), ":"+strconv.Itoa(port))
	if err != nil {
		return err
	}
	return ln.Close()
}

// RunPrecheck 执行完整前置检查，不产生副作用。
func RunPrecheck(remoteHost string, remotePort int, remoteFamily string, localPort int, localFamily string) PrecheckResult {
	var res PrecheckResult
	if err := CheckRemote(remoteHost, remotePort, remoteFamily); err != nil {
		res.RemoteErr = err.Error()
	} else {
		res.RemoteOK = true
	}
	if err := CheckLocalPortFree(localPort, localFamily); err != nil {
		res.LocalErr = err.Error()
	} else {
		res.LocalFree = true
	}
	return res
}
