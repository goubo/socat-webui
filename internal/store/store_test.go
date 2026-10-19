package store

import (
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestProxyCRUD(t *testing.T) {
	s := openTestStore(t)

	p := &Proxy{Name: "db", LocalPort: 21001, RemoteHost: "10.0.0.2", RemotePort: 3306, Enabled: true}
	if err := s.CreateProxy(p); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("创建后未回填 ID")
	}

	got, err := s.GetProxy(p.ID)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if got.Name != "db" || got.LocalPort != 21001 || got.RemoteHost != "10.0.0.2" || got.RemotePort != 3306 {
		t.Fatalf("读回数据不符: %+v", got)
	}

	got.Enabled = false
	got.Name = "db-2"
	if err := s.UpdateProxy(got); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	got2, _ := s.GetProxy(p.ID)
	if got2.Enabled || got2.Name != "db-2" {
		t.Fatalf("更新未生效: %+v", got2)
	}

	list, err := s.ListProxies()
	if err != nil || len(list) != 1 {
		t.Fatalf("列表应含 1 条: %v %v", list, err)
	}

	if err := s.DeleteProxy(p.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := s.GetProxy(p.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("删除后应 NotFound, got %v", err)
	}
}

func TestLocalPortUnique(t *testing.T) {
	s := openTestStore(t)

	first := &Proxy{Name: "a", LocalPort: 21002, RemoteHost: "10.0.0.2", RemotePort: 80}
	if err := s.CreateProxy(first); err != nil {
		t.Fatalf("首条创建失败: %v", err)
	}
	dup := &Proxy{Name: "b", LocalPort: 21002, RemoteHost: "10.0.0.3", RemotePort: 80}
	if err := s.CreateProxy(dup); !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("重复本地端口应返回 ErrDuplicatedKey, got %v", err)
	}
}

func TestEventLifecycle(t *testing.T) {
	s := openTestStore(t)

	p := &Proxy{Name: "ev", LocalPort: 21003, RemoteHost: "10.0.0.4", RemotePort: 80}
	if err := s.CreateProxy(p); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	for _, typ := range []string{EventCreated, EventEnabled, EventExitFailed} {
		if err := s.AddEvent(p.ID, typ, "detail-"+typ); err != nil {
			t.Fatalf("写入事件 %s 失败: %v", typ, err)
		}
	}

	events, err := s.ListEvents(p.ID, 10)
	if err != nil {
		t.Fatalf("查询事件失败: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("应有 3 条事件, got %d", len(events))
	}
	if events[0].Type != EventExitFailed {
		t.Fatalf("事件应按时间倒序, 首条为 %s", events[0].Type)
	}

	if err := s.DeleteProxy(p.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	events, err = s.ListEvents(p.ID, 10)
	if err != nil || len(events) != 0 {
		t.Fatalf("删除代理应级联删除事件, got %d err=%v", len(events), err)
	}
}

func TestSettingKV(t *testing.T) {
	s := openTestStore(t)

	if _, err := s.GetSetting("password_hash"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("未写入前应 NotFound, got %v", err)
	}
	if err := s.SetSetting("password_hash", "hashed-value"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	v, err := s.GetSetting("password_hash")
	if err != nil || v != "hashed-value" {
		t.Fatalf("读回不符: %q err=%v", v, err)
	}
	if err := s.SetSetting("password_hash", "replaced"); err != nil {
		t.Fatalf("覆盖失败: %v", err)
	}
	v, _ = s.GetSetting("password_hash")
	if v != "replaced" {
		t.Fatalf("覆盖未生效: %q", v)
	}
}
