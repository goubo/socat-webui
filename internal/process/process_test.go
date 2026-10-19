package process

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func writeScript(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("写入脚本失败: %v", err)
	}
	return path
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

type statusRecorder struct {
	mu    sync.Mutex
	items []StatusInfo
}

func (r *statusRecorder) record(st StatusInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, st)
}

func (r *statusRecorder) last() StatusInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.items) == 0 {
		return StatusInfo{Status: StatusStopped}
	}
	return r.items[len(r.items)-1]
}

func fastSupervisor(bin string, backoff []time.Duration, onChange func(uint, StatusInfo)) *Supervisor {
	return NewSupervisor(SupervisorConfig{
		BinPath:     bin,
		Backoff:     backoff,
		StableAfter: 10 * time.Second,
		StopGrace:   3 * time.Second,
		OnChange:    onChange,
	})
}

func TestStartAndStop(t *testing.T) {
	script := writeScript(t, "sleep.sh", "#!/bin/sh\nsleep 30\n")
	rec := &statusRecorder{}
	sup := fastSupervisor(script, nil, func(_ uint, st StatusInfo) { rec.record(st) })
	t.Cleanup(sup.StopAll)

	if err := sup.Start(1); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	st, ok := sup.Status(1)
	if !ok || st.Status != StatusRunning || st.PID <= 0 {
		t.Fatalf("启动后应为 running 且有 PID: %+v ok=%v", st, ok)
	}

	if err := sup.Start(1); err == nil {
		t.Fatal("重复启动应报错")
	}

	if err := sup.Stop(1); err != nil {
		t.Fatalf("停止失败: %v", err)
	}
	st, _ = sup.Status(1)
	if st.Status != StatusStopped {
		t.Fatalf("停止后状态应为 stopped: %+v", st)
	}
	if last := rec.last(); last.Status != StatusStopped {
		t.Fatalf("最后回调应为 stopped: %+v", last)
	}
}

func TestStopKillsProcessGroup(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pids")
	script := writeScript(t, "group.sh", "#!/bin/sh\necho $$ > \""+pidfile+"\"\nsleep 30 &\necho $! >> \""+pidfile+"\"\nsleep 30\n")
	sup := fastSupervisor(script, nil, nil)
	t.Cleanup(sup.StopAll)

	if err := sup.Start(7); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		data, err := os.ReadFile(pidfile)
		return err == nil && len(strings.Fields(string(data))) == 2
	}) {
		t.Fatal("等待脚本记录父子 PID 超时")
	}

	if err := sup.Stop(7); err != nil {
		t.Fatalf("停止失败: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	data, _ := os.ReadFile(pidfile)
	for _, field := range strings.Fields(string(data)) {
		var pid int
		if _, err := fmt.Sscanf(field, "%d", &pid); err != nil {
			continue
		}
		if err := syscall.Kill(pid, 0); err == nil {
			t.Fatalf("PID %s 应随进程组一起退出", field)
		}
	}
}

func TestStopEscalatesToSIGKILL(t *testing.T) {
	script := writeScript(t, "trap.sh", "#!/bin/sh\ntrap '' TERM\nsleep 30\n")
	sup := NewSupervisor(SupervisorConfig{
		BinPath:   script,
		StopGrace: 200 * time.Millisecond,
	})
	t.Cleanup(sup.StopAll)

	if err := sup.Start(9); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	st, _ := sup.Status(9)
	pid := st.PID

	begin := time.Now()
	if err := sup.Stop(9); err != nil {
		t.Fatalf("停止失败: %v", err)
	}
	elapsed := time.Since(begin)
	if elapsed > 3*time.Second {
		t.Fatalf("SIGKILL 升级未生效，耗时 %v", elapsed)
	}
	time.Sleep(50 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("进程 %d 应已被 SIGKILL 清理", pid)
	}
}

func TestAbnormalExitBackoffToFailed(t *testing.T) {
	script := writeScript(t, "exit.sh", "#!/bin/sh\necho boom >&2\nexit 7\n")
	rec := &statusRecorder{}
	sup := fastSupervisor(script, []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond},
		func(_ uint, st StatusInfo) { rec.record(st) })

	if err := sup.Start(3); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	if !waitFor(t, 3*time.Second, func() bool {
		st, _ := sup.Status(3)
		return st.Status == StatusFailed
	}) {
		st, _ := sup.Status(3)
		t.Fatalf("3 次重启用尽后应 failed: %+v", st)
	}

	st, _ := sup.Status(3)
	if st.RestartCount != 4 { // 初始运行 + 3 次重启，共 4 次退出
		t.Fatalf("RestartCount 应为 4: %+v", st)
	}
	if !strings.Contains(st.Detail, "退出码 7") || !strings.Contains(st.Detail, "boom") {
		t.Fatalf("Detail 应含退出码与 stderr: %+v", st)
	}

	// failed 后允许重新 Start（替换旧记录）
	if err := sup.Start(3); err != nil {
		t.Fatalf("failed 后重新启动应允许: %v", err)
	}
	_ = sup.Stop(3)
}

func TestStableRunResetsCounter(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	script := writeScript(t, "marker.sh", "#!/bin/sh\nif [ -f \""+marker+"\" ]; then\n  sleep 30\nelse\n  touch \""+marker+"\"\n  exit 1\nfi\n")
	sup := NewSupervisor(SupervisorConfig{
		BinPath:     script,
		Backoff:     []time.Duration{10 * time.Millisecond},
		StableAfter: 120 * time.Millisecond,
		StopGrace:   time.Second,
	})
	t.Cleanup(sup.StopAll)

	if err := sup.Start(5); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	if !waitFor(t, 3*time.Second, func() bool {
		st, _ := sup.Status(5)
		return st.Status == StatusRunning && st.RestartCount == 0
	}) {
		st, _ := sup.Status(5)
		t.Fatalf("重启成功并稳定后计数应清零: %+v", st)
	}
}

func TestStopDuringBackoffCancelsRestart(t *testing.T) {
	script := writeScript(t, "exit.sh", "#!/bin/sh\nexit 1\n")
	sup := fastSupervisor(script, []time.Duration{400 * time.Millisecond}, nil)

	if err := sup.Start(6); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		st, _ := sup.Status(6)
		return st.Status == StatusRestarting
	}) {
		t.Fatal("应进入 restarting")
	}

	if err := sup.Stop(6); err != nil {
		t.Fatalf("退避期停止失败: %v", err)
	}
	time.Sleep(700 * time.Millisecond) // 越过原退避时刻

	st, _ := sup.Status(6)
	if st.Status != StatusStopped {
		t.Fatalf("退避期停止后不应再重启: %+v", st)
	}
}

func TestDetectSocatFake(t *testing.T) {
	script := writeScript(t, "socat", "#!/bin/sh\necho 'socat by Gerhard Rieger and others - version 1.8.0.1 on Linux'\n")
	path, version, err := DetectSocat(script)
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	if version != "1.8.0.1" {
		t.Fatalf("版本解析不符: %q (path=%s)", version, path)
	}
	if _, _, err := DetectSocat(filepath.Join(t.TempDir(), "not-exist")); err == nil {
		t.Fatal("不存在的二进制应报错")
	}
}

func TestDetectSocatReal(t *testing.T) {
	if _, err := exec.LookPath("socat"); err != nil {
		t.Skip("本机未安装 socat")
	}
	path, version, err := DetectSocat("")
	if err != nil {
		t.Fatalf("真实 socat 探测失败: %v", err)
	}
	if path == "" || version == "" || version == "unknown" {
		t.Logf("探测结果 path=%s version=%s", path, version)
	}
}
