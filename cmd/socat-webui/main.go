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
	"socat-webui/internal/server"
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

	httpSrv := &http.Server{Addr: cfg.ListenAddr(), Handler: server.New(cfg)}

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
