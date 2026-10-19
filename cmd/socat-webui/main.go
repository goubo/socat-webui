package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"socat-webui/internal/config"
	"socat-webui/internal/process"
	"socat-webui/internal/proxy"
	"socat-webui/internal/server"
	"socat-webui/internal/store"
	"socat-webui/internal/systemd"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("程序退出", "err", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		switch args[0] {
		case "serve", "install", "uninstall":
			cmd = args[0]
			args = args[1:]
		}
	}
	switch cmd {
	case "install":
		return installCmd(args)
	case "uninstall":
		return systemd.Uninstall()
	default:
		return serve(args)
	}
}

func installCmd(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	listen := fs.String("listen", os.Getenv("SOCAT_WEBUI_LISTEN"), "WebUI 监听地址（默认 0.0.0.0）")
	port := fs.Int("port", config.DefaultPort, "WebUI 监听端口")
	dbPath := fs.String("db", os.Getenv("SOCAT_WEBUI_DB"), "SQLite 数据文件路径（默认 /var/lib/socat-webui/proxies.db）")
	password := fs.String("password", os.Getenv("SOCAT_WEBUI_PASSWORD"), "面板密码；缺省交互输入或首启自动生成")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("解析 install 参数: %w", err)
	}
	if *listen == "" {
		*listen = config.DefaultListen
	}
	if *password == "" && term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Print("设置面板密码: ")
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return fmt.Errorf("读取密码: %w", err)
		}
		*password = strings.TrimSpace(string(raw))
	}
	return systemd.Install(systemd.InstallOptions{
		Listen:   *listen,
		Port:     *port,
		DBPath:   *dbPath,
		Password: *password,
	})
}

func serve(args []string) error {
	cfg, err := config.Parse(args)
	if err != nil {
		return fmt.Errorf("解析配置: %w", err)
	}
	slog.Info("启动 socat-webui", "addr", cfg.ListenAddr(), "db", cfg.DBPath)

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	if _, _, err := process.DetectSocat(cfg.SocatPath); err != nil {
		slog.Warn("socat 不可用，代理管理操作将被拒绝", "err", err)
	}

	mgr := proxy.NewManager(st, proxy.ManagerConfig{
		SocatPath: cfg.SocatPath,
		SelfPort:  cfg.Port,
	}, func(id uint, st process.StatusInfo) {
		slog.Info("代理进程状态变化", "proxyID", id, "status", st.Status, "detail", st.Detail)
	})
	defer mgr.Shutdown()

	auth := server.NewAuthService(st, nil)
	if err := auth.BootstrapPassword(os.Getenv("SOCAT_WEBUI_PASSWORD")); err != nil {
		return fmt.Errorf("初始化面板密码: %w", err)
	}

	// 恢复先于 HTTP 监听：WebUI 可访问即代表恢复已完成（02-design 核心流程）
	mgr.Restore()

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr(),
		Handler:           server.New(cfg, mgr, auth),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		return fmt.Errorf("HTTP 服务退出: %w", err)
	case <-ctx.Done():
		slog.Info("收到退出信号，开始优雅关闭")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("关闭 HTTP 服务: %w", err)
	}
	return nil
}
