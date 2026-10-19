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

// CheckRemote 从本机 TCP dial 远端地址，代表目标服务可用性。
func CheckRemote(host string, port int) error {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

// CheckLocalPortFree 通过临时监听探测本地端口是否空闲。
func CheckLocalPortFree(port int) error {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return err
	}
	return ln.Close()
}

// RunPrecheck 执行完整前置检查，不产生副作用。
func RunPrecheck(host string, remotePort, localPort int) PrecheckResult {
	var res PrecheckResult
	if err := CheckRemote(host, remotePort); err != nil {
		res.RemoteErr = err.Error()
	} else {
		res.RemoteOK = true
	}
	if err := CheckLocalPortFree(localPort); err != nil {
		res.LocalErr = err.Error()
	} else {
		res.LocalFree = true
	}
	return res
}
