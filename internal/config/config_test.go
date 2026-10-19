package config

import (
	"testing"
)

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse 默认配置失败: %v", err)
	}
	if cfg.Listen != DefaultListen || cfg.Port != 15200 || cfg.DBPath != DefaultDBPath {
		t.Fatalf("默认值不符: %+v", cfg)
	}
	if got, want := cfg.ListenAddr(), "0.0.0.0:15200"; got != want {
		t.Fatalf("ListenAddr = %q, want %q", got, want)
	}
}

func TestParseFlagOverride(t *testing.T) {
	cfg, err := Parse([]string{"--listen", "127.0.0.1", "--port", "18080", "--db", "/tmp/x.db"})
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if cfg.Listen != "127.0.0.1" || cfg.Port != 18080 || cfg.DBPath != "/tmp/x.db" {
		t.Fatalf("flag 覆盖未生效: %+v", cfg)
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("SOCAT_WEBUI_LISTEN", "10.0.0.1")
	t.Setenv("SOCAT_WEBUI_PORT", "15201")
	t.Setenv("SOCAT_WEBUI_DB", "/tmp/env.db")

	cfg, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if cfg.Listen != "10.0.0.1" || cfg.Port != 15201 || cfg.DBPath != "/tmp/env.db" {
		t.Fatalf("环境变量覆盖未生效: %+v", cfg)
	}
}

func TestFlagPriorityOverEnv(t *testing.T) {
	t.Setenv("SOCAT_WEBUI_PORT", "15201")

	cfg, err := Parse([]string{"--port", "15202"})
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if cfg.Port != 15202 {
		t.Fatalf("flag 应优先于环境变量, got %d", cfg.Port)
	}
}

func TestInvalidPort(t *testing.T) {
	for _, port := range []string{"0", "65536", "-1"} {
		if _, err := Parse([]string{"--port", port}); err == nil {
			t.Fatalf("端口 %s 应被拒绝", port)
		}
	}
}

func TestInvalidEnvPort(t *testing.T) {
	t.Setenv("SOCAT_WEBUI_PORT", "not-a-number")
	if _, err := Parse(nil); err == nil {
		t.Fatal("非法环境变量端口应报错")
	}
}
