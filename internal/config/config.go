package config

import (
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
)

const (
	DefaultListen = "0.0.0.0"
	DefaultPort   = 15200
	DefaultDBPath = "socat-webui.db"
)

type Config struct {
	Listen    string
	Port      int
	DBPath    string
	SocatPath string // 显式 socat 路径；空则使用 PATH
}

func (c Config) ListenAddr() string {
	return net.JoinHostPort(c.Listen, strconv.Itoa(c.Port))
}

// Parse 解析 serve 参数，优先级：flag > 环境变量（SOCAT_WEBUI_LISTEN/PORT/DB）> 默认值。
func Parse(args []string) (Config, error) {
	cfg := Config{
		Listen:    envOr("SOCAT_WEBUI_LISTEN", DefaultListen),
		Port:      DefaultPort,
		DBPath:    envOr("SOCAT_WEBUI_DB", DefaultDBPath),
		SocatPath: os.Getenv("SOCAT_WEBUI_SOCAT"),
	}
	if v := os.Getenv("SOCAT_WEBUI_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("环境变量 SOCAT_WEBUI_PORT 无效: %w", err)
		}
		cfg.Port = p
	}

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "WebUI 监听地址")
	fs.IntVar(&cfg.Port, "port", cfg.Port, "WebUI 监听端口")
	fs.StringVar(&cfg.DBPath, "db", cfg.DBPath, "SQLite 数据文件路径")
	fs.StringVar(&cfg.SocatPath, "socat-path", cfg.SocatPath, "socat 可执行文件路径（默认查 PATH）")
	if err := fs.Parse(args); err != nil {
		return Config{}, fmt.Errorf("解析启动参数: %w", err)
	}

	if cfg.Port < 1 || cfg.Port > 65535 {
		return Config{}, fmt.Errorf("监听端口超出范围 [1,65535]: %d", cfg.Port)
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
