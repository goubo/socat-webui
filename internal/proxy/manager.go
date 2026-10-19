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
	SelfPort  int // WebUI 自身监听端口，映射端口不得与其相同
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
	Name       string `json:"name"`
	LocalPort  int    `json:"localPort"`
	RemoteHost string `json:"remoteHost"`
	RemotePort int    `json:"remotePort"`
}

// View 是代理定义 + 实际运行状态的聚合视图。
type View struct {
	ID         uint               `json:"id"`
	Name       string             `json:"name"`
	LocalPort  int                `json:"localPort"`
	RemoteHost string             `json:"remoteHost"`
	RemotePort int                `json:"remotePort"`
	Enabled    bool               `json:"enabled"`
	CreatedAt  time.Time          `json:"createdAt"`
	UpdatedAt  time.Time          `json:"updatedAt"`
	Runtime    process.StatusInfo `json:"runtime"`
}

// HealthInfo R-004/R-007 聚合状态。
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
		BinPath:  binPath,
		OnChange: onChange,
	})
	return m
}

// Shutdown 停止全部受管进程（程序退出前调用）。
func (m *Manager) Shutdown() {
	m.sup.StopAll()
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
	if err := ValidateRemoteHost(in.RemoteHost); err != nil {
		return fmt.Errorf("%w: %v", ErrValidation, err)
	}
	return nil
}

// socatAvailable 每次操作前实时探测，socat 可中途安装无需重启（D-004）。
func (m *Manager) socatAvailable() error {
	if _, _, err := process.DetectSocat(m.cfg.SocatPath); err != nil {
		return fmt.Errorf("%w: %v", ErrSocatMissing, err)
	}
	return nil
}

func (m *Manager) view(p store.Proxy) View {
	v := View{
		ID: p.ID, Name: p.Name, LocalPort: p.LocalPort,
		RemoteHost: p.RemoteHost, RemotePort: p.RemotePort,
		Enabled: p.Enabled, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
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
	if err := ValidateRemoteHost(in.RemoteHost); err != nil {
		return PrecheckResult{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if in.RemotePort < 1 || in.RemotePort > 65535 || in.LocalPort < 1 || in.LocalPort > 65535 {
		return PrecheckResult{}, fmt.Errorf("%w: 端口超出范围", ErrValidation)
	}
	return RunPrecheck(in.RemoteHost, in.RemotePort, in.LocalPort), nil
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

// Create 校验 → socat/远端/本地端口检查 → 落库 →（启用时）启动（R-005/R-006）。
func (m *Manager) Create(in ProxyInput, enabled bool) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validate(in); err != nil {
		return View{}, err
	}
	if enabled {
		if err := m.socatAvailable(); err != nil {
			return View{}, err
		}
	}
	res := RunPrecheck(in.RemoteHost, in.RemotePort, in.LocalPort)
	if !res.RemoteOK {
		return View{}, fmt.Errorf("%w: %s", ErrRemoteUnreachable, res.RemoteErr)
	}
	if !res.LocalFree {
		return View{}, fmt.Errorf("%w: %s", ErrPortInUse, res.LocalErr)
	}

	p := &store.Proxy{
		Name: strings.TrimSpace(in.Name), LocalPort: in.LocalPort,
		RemoteHost: in.RemoteHost, RemotePort: in.RemotePort, Enabled: enabled,
	}
	if err := m.st.CreateProxy(p); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return View{}, fmt.Errorf("%w: %d", ErrPortConflict, in.LocalPort)
		}
		return View{}, err
	}
	_ = m.st.AddEvent(p.ID, store.EventCreated, fmt.Sprintf("→ %s:%d", in.RemoteHost, in.RemotePort))

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

	portChanged := in.LocalPort != p.LocalPort
	if portChanged {
		if existing, err := m.st.FindByLocalPort(in.LocalPort); err == nil && existing.ID != id {
			return View{}, fmt.Errorf("%w: 已被代理 %q(%d) 使用", ErrPortConflict, existing.Name, existing.ID)
		}
	}
	if p.Enabled {
		if err := m.socatAvailable(); err != nil {
			return View{}, err
		}
		res := RunPrecheck(in.RemoteHost, in.RemotePort, in.LocalPort)
		if !res.RemoteOK {
			return View{}, fmt.Errorf("%w: %s", ErrRemoteUnreachable, res.RemoteErr)
		}
		if portChanged && !res.LocalFree {
			return View{}, fmt.Errorf("%w: %s", ErrPortInUse, res.LocalErr)
		}
		if st, ok := m.sup.Status(id); ok && st.Status == process.StatusRunning {
			if err := m.sup.Stop(id); err != nil {
				return View{}, fmt.Errorf("停止旧进程失败: %w", err)
			}
		}
	}

	p.Name = strings.TrimSpace(in.Name)
	p.LocalPort = in.LocalPort
	p.RemoteHost = in.RemoteHost
	p.RemotePort = in.RemotePort
	if err := m.st.UpdateProxy(p); err != nil {
		return View{}, err
	}
	_ = m.st.AddEvent(id, store.EventUpdated, fmt.Sprintf("→ %s:%d", in.RemoteHost, in.RemotePort))

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
	res := RunPrecheck(p.RemoteHost, p.RemotePort, p.LocalPort)
	if !res.RemoteOK {
		return View{}, fmt.Errorf("%w: %s", ErrRemoteUnreachable, res.RemoteErr)
	}
	if !res.LocalFree {
		return View{}, fmt.Errorf("%w: %s", ErrPortInUse, res.LocalErr)
	}

	if !p.Enabled {
		p.Enabled = true
		if err := m.st.UpdateProxy(p); err != nil {
			return View{}, err
		}
	}
	if err := m.startLocked(p); err != nil {
		return View{}, err
	}
	_ = m.st.AddEvent(id, store.EventEnabled, "人工启用")
	return m.view(*p), nil
}

// Disable 停止进程并置 enabled=false；进程实际状态为准（R-007）。
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
	if err := m.sup.Stop(id); err != nil {
		return View{}, err
	}
	if p.Enabled {
		p.Enabled = false
		if err := m.st.UpdateProxy(p); err != nil {
			return View{}, err
		}
	}
	_ = m.st.AddEvent(id, store.EventDisabled, "人工停用")
	return m.view(*p), nil
}

// Delete 停止进程并删除定义与事件历史。
func (m *Manager) Delete(id uint) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.st.GetProxy(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	if err := m.sup.Stop(id); err != nil {
		return err
	}
	return m.st.DeleteProxy(id)
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
	err := m.sup.Start(p.ID, SocatArgs(p.LocalPort, p.RemoteHost, p.RemotePort)...)
	if err != nil && errors.Is(err, process.ErrProcessActive) {
		return ErrAlreadyRunning
	}
	return err
}
