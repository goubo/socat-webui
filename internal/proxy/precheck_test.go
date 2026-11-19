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

// hasIPv6Loopback 本机无 IPv6 时跳过 v6 相关用例（部分 CI 容器关闭 IPv6）。
func hasIPv6Loopback(t *testing.T) bool {
	t.Helper()
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("本机无 IPv6 回环，跳过: %v", err)
	}
	ln.Close()
	return true
}

func TestCheckRemote(t *testing.T) {
	port, stop := startEchoListener(t)
	defer stop()

	if err := CheckRemote("127.0.0.1", port, RemoteFamilyAuto); err != nil {
		t.Fatalf("可达远端应通过: %v", err)
	}

	closed := freeTCPPort(t) // 已释放的端口，回环连接立即拒绝
	if err := CheckRemote("127.0.0.1", closed, RemoteFamilyAuto); err == nil {
		t.Fatal("不可达远端应失败")
	}
}

func TestCheckRemoteIPv6(t *testing.T) {
	if !hasIPv6Loopback(t) {
		return
	}
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatalf("监听 IPv6 失败: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	if err := CheckRemote("::1", port, RemoteFamilyAuto); err != nil {
		t.Fatalf("IPv6 远端应可达: %v", err)
	}
}

func TestCheckLocalPortFree(t *testing.T) {
	free := freeTCPPort(t)
	if err := CheckLocalPortFree(free, LocalFamilyIPv4); err != nil {
		t.Fatalf("空闲端口应通过: %v", err)
	}

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()
	used := ln.Addr().(*net.TCPAddr).Port
	if err := CheckLocalPortFree(used, LocalFamilyIPv4); err == nil {
		t.Fatal("被占端口应失败")
	}
}

func TestCheckLocalPortFreeFamilies(t *testing.T) {
	if !hasIPv6Loopback(t) {
		return
	}
	free := freeTCPPort(t)
	for _, family := range []string{LocalFamilyIPv4, LocalFamilyIPv6, LocalFamilyDual} {
		if err := CheckLocalPortFree(free, family); err != nil {
			t.Errorf("%s 空闲端口应通过: %v", family, err)
		}
	}

	// v4-only 监听只挡 v4/dual 检查，不挡 v6-only 检查
	ln, err := net.Listen("tcp4", ":0")
	if err != nil {
		t.Fatalf("监听 IPv4 失败: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if err := CheckLocalPortFree(port, LocalFamilyIPv4); err == nil {
		t.Error("v4 监听下 v4 检查应失败")
	}
	if err := CheckLocalPortFree(port, LocalFamilyIPv6); err != nil {
		t.Errorf("v4 监听下 v6 检查应通过: %v", err)
	}
}

func TestRunPrecheck(t *testing.T) {
	remotePort, stop := startEchoListener(t)
	defer stop()
	localPort := freeTCPPort(t)

	res := RunPrecheck("127.0.0.1", remotePort, RemoteFamilyAuto, localPort, LocalFamilyIPv4)
	if !res.RemoteOK || !res.LocalFree {
		t.Fatalf("全通场景不符: %+v", res)
	}

	res = RunPrecheck("127.0.0.1", freeTCPPort(t), RemoteFamilyAuto, localPort, LocalFamilyIPv4)
	if res.RemoteOK || res.RemoteErr == "" {
		t.Fatalf("远端不通应有错误信息: %+v", res)
	}

	ln, _ := net.Listen("tcp", ":0")
	defer ln.Close()
	res = RunPrecheck("127.0.0.1", remotePort, RemoteFamilyAuto, ln.Addr().(*net.TCPAddr).Port, LocalFamilyIPv4)
	if res.LocalFree || res.LocalErr == "" {
		t.Fatalf("本地被占应有错误信息: %+v", res)
	}
}
