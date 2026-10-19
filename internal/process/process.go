package process

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Status 是代理进程的运行态，始终来自真实进程情况（D-005）。
type Status string

const (
	StatusRunning    Status = "running"
	StatusRestarting Status = "restarting"
	StatusFailed     Status = "failed"
	StatusStopped    Status = "stopped"
)

type StatusInfo struct {
	Status       Status    `json:"status"`
	Detail       string    `json:"detail,omitempty"`
	PID          int       `json:"pid,omitempty"`
	StartedAt    time.Time `json:"startedAt,omitempty"`
	RestartCount int       `json:"restartCount"`
}

// SupervisorConfig 全部时间参数可注入，测试用毫秒级值加速。
type SupervisorConfig struct {
	BinPath     string
	Backoff     []time.Duration // 退避表；长度即重启机会次数，默认 1s/5s/30s
	StableAfter time.Duration   // 连续运行超过该时长后失败计数清零
	StopGrace   time.Duration   // SIGTERM 后多久升级 SIGKILL
	// OnChange 在状态变化时回调（锁外调用）。回调内不要再调用 Supervisor 方法。
	OnChange func(id uint, st StatusInfo)
}

type Supervisor struct {
	cfg   SupervisorConfig
	mu    sync.Mutex
	procs map[uint]*managedProc
}

type managedProc struct {
	args     []string
	cmd      *exec.Cmd
	stderr   *limitedBuffer
	runDone  chan struct{}
	stopping bool
	restarts int
	timer    *time.Timer
	status   StatusInfo
}

var socatVersionRe = regexp.MustCompile(`version (\d+(?:\.\d+)+)`)

// DetectSocat 探测 socat：explicit 为空时查 PATH；返回解析到的版本。
func DetectSocat(explicit string) (path, version string, err error) {
	name := explicit
	if name == "" {
		name = "socat"
	}
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", "", fmt.Errorf("查找 socat: %w", err)
	}
	out, err := exec.Command(resolved, "-V").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("执行 %s -V: %w", resolved, err)
	}
	if m := socatVersionRe.FindSubmatch(out); m != nil {
		return resolved, string(m[1]), nil
	}
	return resolved, "unknown", nil
}

func NewSupervisor(cfg SupervisorConfig) *Supervisor {
	if len(cfg.Backoff) == 0 {
		cfg.Backoff = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second}
	}
	if cfg.StableAfter <= 0 {
		cfg.StableAfter = time.Minute
	}
	if cfg.StopGrace <= 0 {
		cfg.StopGrace = 3 * time.Second
	}
	return &Supervisor{cfg: cfg, procs: make(map[uint]*managedProc)}
}

// Start 启动 id 对应的进程；同一 id 已在运行/重启等待中时返回错误。
func (s *Supervisor) Start(id uint, args ...string) error {
	s.mu.Lock()
	if old := s.procs[id]; old != nil {
		switch old.status.Status {
		case StatusRunning, StatusRestarting:
			s.mu.Unlock()
			return fmt.Errorf("代理 %d 进程已在运行", id)
		}
	}
	p := &managedProc{args: args}
	s.procs[id] = p
	err := s.launchLocked(id, p)
	st := p.status
	s.mu.Unlock()
	s.notify(id, st)
	return err
}

func (s *Supervisor) launchLocked(id uint, p *managedProc) error {
	cmd := exec.Command(s.cfg.BinPath, p.args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr := &limitedBuffer{cap: 512}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		p.status = StatusInfo{Status: StatusFailed, Detail: fmt.Sprintf("启动失败: %v", err)}
		return fmt.Errorf("启动进程: %w", err)
	}
	p.cmd = cmd
	p.stderr = stderr
	p.runDone = make(chan struct{})
	p.status = StatusInfo{Status: StatusRunning, PID: cmd.Process.Pid, StartedAt: time.Now(), RestartCount: p.restarts}
	go s.watch(id, p, cmd)

	if p.restarts > 0 {
		time.AfterFunc(s.cfg.StableAfter, func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if cur := s.procs[id]; cur == p && p.status.Status == StatusRunning {
				p.restarts = 0
				p.status.RestartCount = 0
			}
		})
	}
	return nil
}

// watch 等待进程退出并按 stopping/退避策略推进状态机。
func (s *Supervisor) watch(id uint, p *managedProc, cmd *exec.Cmd) {
	err := cmd.Wait()
	detail := exitDetail(err, p.stderr)

	s.mu.Lock()
	done := p.runDone // 本轮运行的完成信号；解锁后可能被重启的新一轮替换
	if cur := s.procs[id]; cur != p {
		s.mu.Unlock()
		close(done)
		return
	}
	if p.stopping {
		p.status = StatusInfo{Status: StatusStopped, Detail: detail}
		st := p.status
		s.mu.Unlock()
		close(done)
		s.notify(id, st)
		return
	}

	p.restarts++
	if p.restarts > len(s.cfg.Backoff) {
		p.status = StatusInfo{Status: StatusFailed, Detail: detail, RestartCount: p.restarts}
		st := p.status
		s.mu.Unlock()
		close(done)
		s.notify(id, st)
		return
	}

	p.status = StatusInfo{Status: StatusRestarting, Detail: detail, RestartCount: p.restarts}
	delay := s.cfg.Backoff[p.restarts-1]
	p.timer = time.AfterFunc(delay, func() {
		s.mu.Lock()
		if cur := s.procs[id]; cur != p || p.stopping || p.status.Status != StatusRestarting {
			s.mu.Unlock()
			return
		}
		_ = s.launchLocked(id, p)
		st := p.status
		s.mu.Unlock()
		s.notify(id, st)
	})
	st := p.status
	s.mu.Unlock()
	close(done)
	s.notify(id, st)
}

// Stop 停止 id 对应进程：SIGTERM 进程组，超过 StopGrace 升级 SIGKILL；
// 若处于退避等待则直接取消重启并置 stopped。
func (s *Supervisor) Stop(id uint) error {
	s.mu.Lock()
	p := s.procs[id]
	if p == nil {
		s.mu.Unlock()
		return nil
	}
	p.stopping = true
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	if p.status.Status != StatusRunning {
		if p.status.Status == StatusRestarting {
			p.status = StatusInfo{Status: StatusStopped, Detail: "退避期间人工停止"}
		}
		st := p.status
		s.mu.Unlock()
		s.notify(id, st)
		return nil
	}
	pid := p.cmd.Process.Pid
	runDone := p.runDone
	grace := s.cfg.StopGrace
	s.mu.Unlock()

	_ = killProcessGroup(pid, syscall.SIGTERM)
	select {
	case <-runDone:
	case <-time.After(grace):
		_ = killProcessGroup(pid, syscall.SIGKILL)
		<-runDone
	}
	return nil
}

// StopAll 停止全部受管进程（程序退出时调用）。
func (s *Supervisor) StopAll() {
	s.mu.Lock()
	ids := make([]uint, 0, len(s.procs))
	for id := range s.procs {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		_ = s.Stop(id)
	}
}

func (s *Supervisor) Status(id uint) (StatusInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.procs[id]
	if !ok {
		return StatusInfo{Status: StatusStopped}, false
	}
	return p.status, true
}

func (s *Supervisor) notify(id uint, st StatusInfo) {
	if s.cfg.OnChange != nil {
		s.cfg.OnChange(id, st)
	}
}

func exitDetail(err error, stderr *limitedBuffer) string {
	detail := "正常退出"
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		detail = fmt.Sprintf("异常退出，退出码 %d", ee.ExitCode())
	case err != nil:
		detail = fmt.Sprintf("异常退出: %v", err)
	}
	if tail := stderr.Tail(); tail != "" {
		detail += "；stderr: " + tail
	}
	return detail
}

// limitedBuffer 并发安全地保留最多 cap 字节的尾部输出（Write 永不报错）。
type limitedBuffer struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cap <= 0 {
		b.cap = 512
	}
	room := b.cap - len(b.buf)
	if room > 0 {
		n := min(room, len(p))
		b.buf = append(b.buf, p[:n]...)
	}
	return len(p), nil
}

func (b *limitedBuffer) Tail() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.buf))
}
