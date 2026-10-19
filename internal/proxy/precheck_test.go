package proxy

import (
	"net"
	"testing"
)

func startEchoListener(t *testing.T) (port int, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					if _, err := c.Write(buf[:n]); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() { ln.Close() }
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func TestCheckRemote(t *testing.T) {
	port, stop := startEchoListener(t)
	defer stop()

	if err := CheckRemote("127.0.0.1", port); err != nil {
		t.Fatalf("可达远端应通过: %v", err)
	}

	closed := freeTCPPort(t) // 已释放的端口，回环连接立即拒绝
	if err := CheckRemote("127.0.0.1", closed); err == nil {
		t.Fatal("不可达远端应失败")
	}
}

func TestCheckLocalPortFree(t *testing.T) {
	free := freeTCPPort(t)
	if err := CheckLocalPortFree(free); err != nil {
		t.Fatalf("空闲端口应通过: %v", err)
	}

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()
	used := ln.Addr().(*net.TCPAddr).Port
	if err := CheckLocalPortFree(used); err == nil {
		t.Fatal("被占端口应失败")
	}
}

func TestRunPrecheck(t *testing.T) {
	remotePort, stop := startEchoListener(t)
	defer stop()
	localPort := freeTCPPort(t)

	res := RunPrecheck("127.0.0.1", remotePort, localPort)
	if !res.RemoteOK || !res.LocalFree {
		t.Fatalf("全通场景不符: %+v", res)
	}

	res = RunPrecheck("127.0.0.1", freeTCPPort(t), localPort)
	if res.RemoteOK || res.RemoteErr == "" {
		t.Fatalf("远端不通应有错误信息: %+v", res)
	}

	ln, _ := net.Listen("tcp", ":0")
	defer ln.Close()
	res = RunPrecheck("127.0.0.1", remotePort, ln.Addr().(*net.TCPAddr).Port)
	if res.LocalFree || res.LocalErr == "" {
		t.Fatalf("本地被占应有错误信息: %+v", res)
	}
}
