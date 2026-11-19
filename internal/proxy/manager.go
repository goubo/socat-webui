package proxy

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"socat-webui/internal/process"
	"socat-webui/internal/store"
)

// ManagerConfig 管理器配置；SocatPath 为空时使用 PATH 中的 socat。
type ManagerConfig struct {
	SocatPath string
	SelfPort  int             // WebUI 自身监听端口，映射端口不得与其相同
	Backoff   []time.Duration // 测试注入毫秒级退避；空则用监督器默认 1s/5s/30s
}

// Manager 编排持久化、前置检查与 socat 进程监督；
// 管理操作（增删改启停）串行化，避免同一本地端口的检查-启动竞态。
type Manager struct {
	cfg ManagerConfig
	st  *store.Store
	sup *process.Supervisor
	mu  sync.Mutex
}

// ProxyInput 创建/修改代理的请求体。
type ProxyInput struct {
	Name         string `json:"name"`
	LocalPort    int    `json:"localPort"`
	LocalFamily  string `json:"localFamily"`
	RemoteHost   string `json:"remoteHost"`
	RemotePort   int    `json:"remotePort"`
	RemoteFamily string `json:"remoteFamily"`
}

// View 是代理定义 + 实际运行状态的聚合视图。
type View struct {
	ID           uint               `json:"id"`
	Name         string             `json:"name"`
	LocalPort    int                `json:"localPort"`
	LocalFamily  string             `json:"localFamily"`
	RemoteHost   string             `json:"remoteHost"`
	RemotePort   int                `json:"remotePort"`
	RemoteFamily string             `json:"remoteFamily"`
	Enabled      bool               `json:"enabled"`
	CreatedAt    time.Time          `json:"createdAt"`
	UpdatedAt    time.Time          `json:"updatedAt"`
	Runtime      process.StatusInfo `json:"runtime"`
}

// HealthInfo 聚合状态。
type HealthInfo struct {
	Socat struct {
		Available bool   `json:"available"`
		Path      string `json:"path"`
		Version   string `json:"version"`
		Error     string `json:"error,omitempty"`
	} `json:"socat"`
	Proxies struct {
		Total   int `json:"total"`
		Running int `json:"running"`
		Failed  int `json:"failed"`
	} `json:"proxies"`
}

func NewManager(st *store.Store, cfg ManagerConfig, onChange func(id uint, st process.StatusInfo)) *Manager {
	binPath := cfg.SocatPath
	if binPath == "" {
		binPath = "socat"
	}
	m := &Manager{cfg: cfg, st: st}
	m.sup = process.NewSupervisor(process.SupervisorConfig{
		BinPath: binPath,
		Backoff: cfg.Backoff,
		OnChange: func(id uint, st process.StatusInfo) {
			if st.Status == process.StatusFailed {
				if err := m.st.AddEvent(id, store.EventExitFailed, st.Detail); err != nil {
					slog.Warn("写入异常退出事件失败", "proxyID", id, "err", err)
				}
			}
			if onChange != nil {
				onChange(id, st)
			}
		},
	})
	return m
}

// Restore 程序启动时按持久化期望状态恢复代理：
// enabled 的重新拉起，端口被占或 socat 缺失标记 failed 并记录事件，不阻塞其他代理；
// 远端不可达不阻止启动（socat fork 为懒连接）。
func (m *Manager) Restore() {
	m.mu.Lock()
	defer m.mu.Unlock()

	proxies, err := m.st.ListProxies()
	if err != nil {
		slog.Error("读取待恢复代理失败", "err", err)
		return
	}

	socatErr := m.socatAvailable()
	var restored, skipped, failedCount int
	for _, p := range proxies {
		if !p.Enabled {
			skipped++
			continue
		}
		if socatErr != nil {
			m.sup.MarkFailed(p.ID, fmt.Sprintf("恢复失败：%v", socatErr))
			_ = m.st.AddEvent(p.ID, store.EventRestoreFailed, socatErr.Error())
			failedCount++
			continue
		}
		if err := CheckLocalPortFree(p.LocalPort, p.LocalFamily); err != nil {
			detail := fmt.Sprintf("恢复失败：本地端口 %d 被占用: %v", p.LocalPort, err)
			m.sup.MarkFailed(p.ID, detail)
			_ = m.st.AddEvent(p.ID, store.EventRestoreFailed, detail)
			failedCount++
			continue
		}
		if err := m.startLocked(&p); err != nil {
			_ = m.st.AddEvent(p.ID, store.EventRestoreFailed, err.Error())
			failedCount++
			continue
		}
		_ = m.st.AddEvent(p.ID, store.EventEnabled, "重启后自动恢复")
		restored++
	}
	slog.Info("重启恢复完成", "restored", restored, "skipped", skipped, "failed", failedCount)
}

// Shutdown 停止全部受管进程（程序退出前调用）。
func (m *Manager) Shutdown() {
	m.sup.StopAll()
}

// normalizeInput 规范化请求体：远端地址、协议族缺省值与取值合法性；
// 调用方应使用返回值，避免括号形态与空协议族落库。
func normalizeInput(in ProxyInput) (ProxyInput, error) {
	host, err := NormalizeRemoteHost(in.RemoteHost)
	if err != nil {
		return in, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	in.RemoteHost = host
	in.LocalFamily = NormalizeLocalFamily(in.LocalFamily)
	in.RemoteFamily = NormalizeRemoteFamily(in.RemoteFamily)
	if err := ValidateFamilies(in.LocalFamily, in.RemoteFamily, host); err != nil {
		return in, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	return in, nil
}

func (m *Manager) validate(in ProxyInput) error {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 64 {
		return fmt.Errorf("%w: 名称必填且不超过 64 字符", ErrValidation)
	}
	if in.LocalPort < 1 || in.LocalPort > 65535 {
		return fmt.Errorf("%w: 本地端口超出范围 [1,65535]", ErrValidation)
	}
	if in.LocalPort == m.cfg.SelfPort {
		return fmt.Errorf("%w: 本地端口与 WebUI 监听端口 %d 冲突", ErrValidation, m.cfg.SelfPort)
	}
	if in.RemotePort < 1 || in.RemotePort > 65535 {
		return fmt.Errorf("%w: 远端端口超出范围 [1,65535]", ErrValidation)
	}
	_, err := normalizeInput(in)
	return err
}

// socatAvailable 每次操作前实时探测，socat 可中途安装无需重启。
func (m *Manager) socatAvailable() error {
	if _, _, err := process.DetectSocat(m.cfg.SocatPath); err != nil {
		return fmt.Errorf("%w: %v", ErrSocatMissing, err)
	}
	return nil
}

func (m *Manager) view(p store.Proxy) View {
	v := View{
		ID: p.ID, Name: p.Name, LocalPort: p.LocalPort,
		LocalFamily: NormalizeLocalFamily(p.LocalFamily),
		RemoteHost:  p.RemoteHost, RemotePort: p.RemotePort,
		RemoteFamily: NormalizeRemoteFamily(p.RemoteFamily),
		Enabled:      p.Enabled, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
	if st, ok := m.sup.Status(p.ID); ok {
		v.Runtime = st
	} else {
		v.Runtime = process.StatusInfo{Status: process.StatusStopped}
	}
	return v
}

// Precheck 独立前置检查入口（表单即时反馈），不产生副作用。
func (m *Manager) Precheck(in ProxyInput) (PrecheckResult, error) {
	if in.RemotePort < 1 || in.RemotePort > 65535 || in.LocalPort < 1 || in.LocalPort > 65535 {
		return PrecheckResult{}, fmt.Errorf("%w: 端口超出范围", ErrValidation)
	}
	normalized, err := normalizeInput(in)
	if err != nil {
		return PrecheckResult{}, err
	}
	return RunPrecheck(normalized.RemoteHost, normalized.RemotePort, normalized.RemoteFamily,
		normalized.LocalPort, normalized.LocalFamily), nil
}

func (m *Manager) List() ([]View, error) {
	list, err := m.st.ListProxies()
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(list))
	for _, p := range list {
		views = append(views, m.view(p))
	}
	return views, nil
}

func (m *Manager) Get(id uint) (View, []store.Event, error) {
	p, err := m.st.GetProxy(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return View{}, nil, ErrNotFound
		}
		return View{}, nil, err
	}
	events, err := m.st.ListEvents(id, 20)
	if err != nil {
		return View{}, nil, err
	}
	return m.view(*p), events, nil
}

// Create 校验 → socat/远端/本地端口检查 → 落库 →（启用时）启动。
func (m *Manager) Create(in ProxyInput, enabled bool) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validate(in); err != nil {
		return View{}, err
	}
	// 先查库判重再探测端口：已被其他代理使用的端口给出精确的冲突信息，
	// 也避免运行中的同款监听干扰本地端口探测（不同平台 SO_REUSEADDR 语义不一）。
	if existing, err := m.st.FindByLocalPort(in.LocalPort); err == nil {
		return View{}, fmt.Errorf("%w: 已被代理 %q(%d) 使用", ErrPortConflict, existing.Name, existing.ID)
	}
	if enabled {
		if err := m.socatAvailable(); err != nil {
			return View{}, err
		}
	}
	in, _ = normalizeInput(in)
	res := RunPrecheck(in.RemoteHost, in.RemotePort, in.RemoteFamily, in.LocalPort, in.LocalFamily)
	if !res.RemoteOK {
		return View{}, fmt.Errorf("%w: %s", ErrRemoteUnreachable, res.RemoteErr)
	}
	if !res.LocalFree {
		return View{}, fmt.Errorf("%w: %s", ErrPortInUse, res.LocalErr)
	}

	p := &store.Proxy{
		Name: strings.TrimSpace(in.Name), LocalPort: in.LocalPort,
		LocalFamily: in.LocalFamily, RemoteHost: in.RemoteHost,
		RemotePort: in.RemotePort, RemoteFamily: in.RemoteFamily, Enabled: enabled,
	}
	if err := m.st.CreateProxy(p); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return View{}, fmt.Errorf("%w: %d", ErrPortConflict, in.LocalPort)
		}
		return View{}, err
	}
	_ = m.st.AddEvent(p.ID, store.EventCreated, "→ "+FormatRemoteHost(in.RemoteHost, in.RemotePort))

	if enabled {
		if err := m.startLocked(p); err != nil {
			slog.Warn("创建后启动失败", "proxyID", p.ID, "err", err)
		} else {
			_ = m.st.AddEvent(p.ID, store.EventEnabled, "创建后自动启动")
		}
	}
	return m.view(*p), nil
}

// Update 修改代理定义；运行中的代理停止后按新定义重启。
func (m *Manager) Update(id uint, in ProxyInput) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, err := m.st.GetProxy(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return View{}, ErrNotFound
		}
		return View{}, err
	}
	if err := m.validate(in); err != nil {
		return View{}, err
	}
	in, _ = normalizeInput(in)
	old := *p
	old.LocalFamily = NormalizeLocalFamily(old.LocalFamily)
	old.RemoteFamily = NormalizeRemoteFamily(old.RemoteFamily)
	wasActive := false
	active := func() bool {
		st, ok := m.sup.Status(id)
		return ok && (st.Status == process.StatusRunning || st.Status == process.StatusRestarting)
	}

	portChanged := in.LocalPort != p.LocalPort || in.LocalFamily != old.LocalFamily
	if portChanged {
		if existing, err := m.st.FindByLocalPort(in.LocalPort); err == nil && existing.ID != id {
			return View{}, fmt.Errorf("%w: 已被代理 %q(%d) 使用", ErrPortConflict, existing.Name, existing.ID)
		}
	}
	if p.Enabled {
		if err := m.socatAvailable(); err != nil {
			return View{}, err
		}
		res := RunPrecheck(in.RemoteHost, in.RemotePort, in.RemoteFamily, in.LocalPort, in.LocalFamily)
		if !res.RemoteOK {
			return View{}, fmt.Errorf("%w: %s", ErrRemoteUnreachable, res.RemoteErr)
		}
		// 同端口仅换监听族时，旧进程自身监听会干扰端口占用检查，须先停再查。
		samePortFamilyChange := portChanged && in.LocalPort == p.LocalPort
		if samePortFamilyChange && active() {
			if err := m.sup.Stop(id); err != nil {
				return View{}, fmt.Errorf("停止旧进程失败: %w", err)
			}
			wasActive = true
			if err := CheckLocalPortFree(in.LocalPort, in.LocalFamily); err != nil {
				if restartErr := m.startLocked(&old); restartErr != nil {
					slog.Error("占用检查失败后恢复旧进程失败", "proxyID", id, "err", restartErr)
				}
				return View{}, fmt.Errorf("%w: %v", ErrPortInUse, err)
			}
		} else if portChanged && !res.LocalFree {
			return View{}, fmt.Errorf("%w: %s", ErrPortInUse, res.LocalErr)
		}
		if !wasActive && active() {
			if err := m.sup.Stop(id); err != nil {
				return View{}, fmt.Errorf("停止旧进程失败: %w", err)
			}
			wasActive = true
		}
	}

	p.Name = strings.TrimSpace(in.Name)
	p.LocalPort = in.LocalPort
	p.LocalFamily = in.LocalFamily
	p.RemoteHost = in.RemoteHost
	p.RemotePort = in.RemotePort
	p.RemoteFamily = in.RemoteFamily
	if err := m.st.UpdateProxy(p); err != nil {
		if wasActive {
			if restartErr := m.startLocked(&old); restartErr != nil {
				slog.Error("更新落库失败后恢复旧进程失败", "proxyID", id, "err", restartErr)
			}
		}
		return View{}, err
	}
	_ = m.st.AddEvent(id, store.EventUpdated, "→ "+FormatRemoteHost(in.RemoteHost, in.RemotePort))

	if p.Enabled {
		if err := m.startLocked(p); err != nil {
			slog.Warn("更新后重启失败", "proxyID", id, "err", err)
		}
	}
	return m.view(*p), nil
}

// Enable 前置检查后启动代理并置期望状态 enabled（幂等：运行中则仅确保标志位）。
func (m *Manager) Enable(id uint) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, err := m.st.GetProxy(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return View{}, ErrNotFound
		}
		return View{}, err
	}
	if st, ok := m.sup.Status(id); ok && st.Status == process.StatusRunning {
		if !p.Enabled {
			p.Enabled = true
			if err := m.st.UpdateProxy(p); err != nil {
				return View{}, err
			}
		}
		return m.view(*p), nil
	}

	if err := m.socatAvailable(); err != nil {
		return View{}, err
	}
	res := RunPrecheck(p.RemoteHost, p.RemotePort, p.RemoteFamily, p.LocalPort, p.LocalFamily)
	if !res.RemoteOK {
		return View{}, fmt.Errorf("%w: %s", ErrRemoteUnreachable, res.RemoteErr)
	}
	if !res.LocalFree {
		return View{}, fmt.Errorf("%w: %s", ErrPortInUse, res.LocalErr)
	}

	wasEnabled := p.Enabled
	if !wasEnabled {
		p.Enabled = true
		if err := m.st.UpdateProxy(p); err != nil {
			return View{}, err
		}
	}
	if err := m.startLocked(p); err != nil {
		if !wasEnabled {
			p.Enabled = false
			if saveErr := m.st.UpdateProxy(p); saveErr != nil {
				slog.Error("启用失败后回滚期望状态失败", "proxyID", id, "err", saveErr)
			}
		}
		return View{}, err
	}
	_ = m.st.AddEvent(id, store.EventEnabled, "人工启用")
	return m.view(*p), nil
}

// Disable 停止进程并置 enabled=false；进程实际状态为准。
func (m *Manager) Disable(id uint) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, err := m.st.GetProxy(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return View{}, ErrNotFound
		}
		return View{}, err
	}
	wasEnabled := p.Enabled
	if wasEnabled {
		p.Enabled = false
		if err := m.st.UpdateProxy(p); err != nil {
			return View{}, err
		}
	}
	if err := m.sup.Stop(id); err != nil {
		if wasEnabled {
			p.Enabled = true
			if saveErr := m.st.UpdateProxy(p); saveErr != nil {
				slog.Error("停用失败后回滚期望状态失败", "proxyID", id, "err", saveErr)
			}
		}
		return View{}, err
	}
	_ = m.st.AddEvent(id, store.EventDisabled, "人工停用")
	return m.view(*p), nil
}

// Delete 停止进程并删除定义与事件历史。
func (m *Manager) Delete(id uint) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, err := m.st.GetProxy(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	st, ok := m.sup.Status(id)
	wasActive := ok && (st.Status == process.StatusRunning || st.Status == process.StatusRestarting)
	if err := m.sup.Stop(id); err != nil {
		return err
	}
	if err := m.st.DeleteProxy(id); err != nil {
		if wasActive {
			if restartErr := m.startLocked(p); restartErr != nil {
				slog.Error("删除失败后恢复代理进程失败", "proxyID", id, "err", restartErr)
			}
		}
		return err
	}
	return nil
}

// Health 实时探测 socat 并聚合代理运行统计。
func (m *Manager) Health() HealthInfo {
	var h HealthInfo
	if path, version, err := process.DetectSocat(m.cfg.SocatPath); err != nil {
		h.Socat.Error = err.Error()
	} else {
		h.Socat.Available, h.Socat.Path, h.Socat.Version = true, path, version
	}
	if list, err := m.st.ListProxies(); err == nil {
		h.Proxies.Total = len(list)
		for _, p := range list {
			if st, ok := m.sup.Status(p.ID); ok {
				switch st.Status {
				case process.StatusRunning:
					h.Proxies.Running++
				case process.StatusFailed:
					h.Proxies.Failed++
				}
			}
		}
	}
	return h
}

func (m *Manager) startLocked(p *store.Proxy) error {
	args, err := SocatArgs(p.LocalPort, p.LocalFamily, p.RemoteHost, p.RemotePort, p.RemoteFamily)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if err := m.sup.Start(p.ID, args...); err != nil {
		if errors.Is(err, process.ErrProcessActive) {
			return ErrAlreadyRunning
		}
		return err
	}
	return nil
}
