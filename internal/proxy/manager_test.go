package proxy

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"socat-webui/internal/store"
)

func openManagerStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "mgr.db"))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func mustSocat(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("socat"); err != nil {
		t.Skip("本机未安装 socat，跳过恢复集成测试")
	}
}

func dialEcho(t *testing.T, port int, payload string) string {
	t.Helper()
	if !waitForPort(t, port, 3*time.Second) {
		t.Fatalf("端口 %d 未就绪", port)
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("连接 %d 失败: %v", port, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("读取回显失败: %v", err)
	}
	return string(buf[:n])
}

func waitForPort(t *testing.T, port int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(30 * time.Millisecond)
	}
	return false
}

func waitStatus(t *testing.T, m *Manager, id uint, want string) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st, ok := m.sup.Status(id); ok && string(st.Status) == want {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	st, _ := m.sup.Status(id)
	t.Fatalf("等待状态 %s 超时, 当前 %+v", want, st)
	return false
}

func TestRestoreEnabledProxies(t *testing.T) {
	mustSocat(t)
	st := openManagerStore(t)
	echoPort, stopEcho := startEchoListener(t)
	defer stopEcho()

	portA, portB := freeTCPPort(t), freeTCPPort(t)
	on := &store.Proxy{Name: "on", LocalPort: portA, RemoteHost: "127.0.0.1", RemotePort: echoPort, Enabled: true}
	off := &store.Proxy{Name: "off", LocalPort: portB, RemoteHost: "127.0.0.1", RemotePort: echoPort, Enabled: false}
	if err := st.CreateProxy(on); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateProxy(off); err != nil {
		t.Fatal(err)
	}

	m := NewManager(st, ManagerConfig{SelfPort: 15200}, nil)
	m.Restore()
	defer m.Shutdown()

	waitStatus(t, m, on.ID, "running")
	if got := dialEcho(t, portA, "restore-1"); got != "restore-1" {
		t.Fatalf("恢复后转发不符: %q", got)
	}
	if st, ok := m.sup.Status(off.ID); ok && st.Status == "running" {
		t.Fatal("未启用代理不应被恢复")
	}

	// 模拟服务重启：停掉全部进程后用同一 DB 重建管理器并恢复
	pidBefore := func() int {
		s, _ := m.sup.Status(on.ID)
		return s.PID
	}()
	m.Shutdown()
	m2 := NewManager(st, ManagerConfig{SelfPort: 15200}, nil)
	m2.Restore()
	defer m2.Shutdown()

	waitStatus(t, m2, on.ID, "running")
	if got := dialEcho(t, portA, "restore-2"); got != "restore-2" {
		t.Fatalf("重启恢复后转发不符: %q", got)
	}
	s, _ := m2.sup.Status(on.ID)
	if s.PID == pidBefore {
		t.Fatalf("重启恢复应是新进程: pid %d == %d", s.PID, pidBefore)
	}
}

func TestRestorePortOccupied(t *testing.T) {
	mustSocat(t)
	st := openManagerStore(t)
	echoPort, stopEcho := startEchoListener(t)
	defer stopEcho()

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	occupied := ln.Addr().(*net.TCPAddr).Port

	p := &store.Proxy{Name: "busy", LocalPort: occupied, RemoteHost: "127.0.0.1", RemotePort: echoPort, Enabled: true}
	if err := st.CreateProxy(p); err != nil {
		t.Fatal(err)
	}

	m := NewManager(st, ManagerConfig{SelfPort: 15200}, nil)
	defer m.Shutdown()
	m.Restore()

	st1, ok := m.sup.Status(p.ID)
	if !ok || st1.Status != "failed" || !strings.Contains(st1.Detail, "被占用") {
		t.Fatalf("端口被占应 failed 且含原因: %+v ok=%v", st1, ok)
	}
	events, err := st.ListEvents(p.ID, 10)
	if err != nil || len(events) == 0 || events[0].Type != store.EventRestoreFailed {
		t.Fatalf("应记录 restore_failed 事件: %v %v", events, err)
	}

	// failed 后 enable 仍可人工重试（端口仍被占 → 返回占用错误）
	if _, err := m.Enable(p.ID); err == nil {
		t.Fatal("端口仍被占时启用应失败")
	}
}

func TestRestoreSocatMissing(t *testing.T) {
	st := openManagerStore(t)
	echoPort, stopEcho := startEchoListener(t)
	defer stopEcho()

	p := &store.Proxy{Name: "nosocat", LocalPort: freeTCPPort(t), RemoteHost: "127.0.0.1", RemotePort: echoPort, Enabled: true}
	if err := st.CreateProxy(p); err != nil {
		t.Fatal(err)
	}

	m := NewManager(st, ManagerConfig{SocatPath: "/nonexistent/socat-bin", SelfPort: 15200}, nil)
	defer m.Shutdown()
	m.Restore() // 不应 panic

	st1, ok := m.sup.Status(p.ID)
	if !ok || st1.Status != "failed" || !strings.Contains(st1.Detail, "socat") {
		t.Fatalf("socat 缺失应 failed: %+v ok=%v", st1, ok)
	}
	events, _ := st.ListEvents(p.ID, 10)
	if len(events) == 0 || events[0].Type != store.EventRestoreFailed {
		t.Fatalf("应记录 restore_failed 事件: %+v", events)
	}
}

func TestExitFailedEventWritten(t *testing.T) {
	st := openManagerStore(t)
	echoPort, stopEcho := startEchoListener(t)
	defer stopEcho()

	script := filepath.Join(t.TempDir(), "fake-socat")
	content := "#!/bin/sh\nif [ \"$1\" = \"-V\" ]; then echo \"socat version 1.8.0.0\"; exit 0; fi\nexit 3\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	p := &store.Proxy{Name: "crash", LocalPort: freeTCPPort(t), RemoteHost: "127.0.0.1", RemotePort: echoPort, Enabled: true}
	if err := st.CreateProxy(p); err != nil {
		t.Fatal(err)
	}

	m := NewManager(st, ManagerConfig{SocatPath: script, SelfPort: 15200, Backoff: []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}}, nil)
	defer m.Shutdown()
	m.Restore()
	waitStatus(t, m, p.ID, "failed")

	events, err := st.ListEvents(p.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var hasExitFailed bool
	for _, ev := range events {
		if ev.Type == store.EventExitFailed {
			hasExitFailed = true
			if !strings.Contains(ev.Detail, "退出码 3") {
				t.Fatalf("exit_failed 详情应含退出码: %+v", ev)
			}
		}
	}
	if !hasExitFailed {
		t.Fatalf("应记录 exit_failed 事件: %+v", events)
	}
}
