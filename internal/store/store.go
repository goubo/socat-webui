package store

import (
	"fmt"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Proxy 一条端口映射定义；Enabled 是期望状态，恢复流程的唯一依据。
type Proxy struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	Name       string    `gorm:"size:64;not null" json:"name"`
	LocalPort  int       `gorm:"not null;uniqueIndex" json:"localPort"`
	RemoteHost string    `gorm:"size:255;not null" json:"remoteHost"`
	RemotePort int       `gorm:"not null" json:"remotePort"`
	Enabled    bool      `gorm:"not null;default:true" json:"enabled"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// Event 代理生命周期事件（创建/启停/异常退出/恢复失败等）。
type Event struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ProxyID   uint      `gorm:"index" json:"proxyId"`
	Type      string    `gorm:"size:32" json:"type"`
	Detail    string    `gorm:"size:512" json:"detail"`
	CreatedAt time.Time `json:"createdAt"`
}

const (
	EventCreated       = "created"
	EventUpdated       = "updated"
	EventEnabled       = "enabled"
	EventDisabled      = "disabled"
	EventDeleted       = "deleted"
	EventExitFailed    = "exit_failed"
	EventRestoreFailed = "restore_failed"
)

// Setting 通用 KV 配置（当前仅存密码哈希，key = password_hash）。
type Setting struct {
	Key   string `gorm:"primaryKey;size:64" json:"key"`
	Value string `json:"value"`
}

type Store struct {
	db *gorm.DB
}

// Open 打开（或创建）SQLite 并迁移表结构；busy_timeout 防偶发写锁冲突。
func Open(path string) (*Store, error) {
	db, err := gorm.Open(sqlite.Open(path+"?_pragma=busy_timeout(3000)"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite %s: %w", path, err)
	}
	if err := db.AutoMigrate(&Proxy{}, &Event{}, &Setting{}); err != nil {
		return nil, fmt.Errorf("迁移表结构: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return fmt.Errorf("获取底层连接: %w", err)
	}
	return sqlDB.Close()
}

func (s *Store) CreateProxy(p *Proxy) error {
	if err := s.db.Create(p).Error; err != nil {
		return fmt.Errorf("创建代理: %w", err)
	}
	return nil
}

func (s *Store) ListProxies() ([]Proxy, error) {
	var list []Proxy
	if err := s.db.Order("local_port").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("查询代理列表: %w", err)
	}
	return list, nil
}

func (s *Store) GetProxy(id uint) (*Proxy, error) {
	var p Proxy
	if err := s.db.First(&p, id).Error; err != nil {
		return nil, fmt.Errorf("查询代理 %d: %w", id, err)
	}
	return &p, nil
}

func (s *Store) FindByLocalPort(port int) (*Proxy, error) {
	var p Proxy
	if err := s.db.First(&p, "local_port = ?", port).Error; err != nil {
		return nil, fmt.Errorf("按本地端口 %d 查询: %w", port, err)
	}
	return &p, nil
}

func (s *Store) UpdateProxy(p *Proxy) error {
	if err := s.db.Save(p).Error; err != nil {
		return fmt.Errorf("更新代理 %d: %w", p.ID, err)
	}
	return nil
}

// DeleteProxy 删除代理定义及其事件；进程停止由上层负责。
func (s *Store) DeleteProxy(id uint) error {
	if err := s.db.Delete(&Proxy{}, id).Error; err != nil {
		return fmt.Errorf("删除代理 %d: %w", id, err)
	}
	if err := s.db.Where("proxy_id = ?", id).Delete(&Event{}).Error; err != nil {
		return fmt.Errorf("删除代理 %d 事件: %w", id, err)
	}
	return nil
}

func (s *Store) AddEvent(proxyID uint, typ, detail string) error {
	if len(detail) > 512 {
		detail = detail[:512]
	}
	if err := s.db.Create(&Event{ProxyID: proxyID, Type: typ, Detail: detail}).Error; err != nil {
		return fmt.Errorf("写入事件: %w", err)
	}
	return nil
}

func (s *Store) ListEvents(proxyID uint, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 20
	}
	var list []Event
	if err := s.db.Where("proxy_id = ?", proxyID).Order("id DESC").Limit(limit).Find(&list).Error; err != nil {
		return nil, fmt.Errorf("查询事件: %w", err)
	}
	return list, nil
}

func (s *Store) GetSetting(key string) (string, error) {
	var st Setting
	if err := s.db.First(&st, "key = ?", key).Error; err != nil {
		return "", fmt.Errorf("读取配置 %s: %w", key, err)
	}
	return st.Value, nil
}

func (s *Store) SetSetting(key, value string) error {
	if err := s.db.Save(&Setting{Key: key, Value: value}).Error; err != nil {
		return fmt.Errorf("写入配置 %s: %w", key, err)
	}
	return nil
}
