package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"socat-webui/internal/config"
	"socat-webui/internal/process"
	"socat-webui/internal/proxy"
	"socat-webui/internal/server"
	"socat-webui/internal/store"
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
	case "install", "uninstall":
		return fmt.Errorf("%s 子命令尚未实现，当前版本仅支持 serve", cmd)
	default:
		return serve(args)
	}
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

	httpSrv := &http.Server{Addr: cfg.ListenAddr(), Handler: server.New(cfg, mgr, auth)}

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
