package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"trades-ai/internal/app"
	"trades-ai/internal/config"
	"trades-ai/internal/log"
	"trades-ai/internal/store"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "配置文件路径，默认使用 configs/config.yaml")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	logger, err := log.NewLogger(cfg.Logging)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化日志失败: %v\n", err)
		os.Exit(1)
	}
	defer func(logger *zap.Logger) {
		_ = logger.Sync()
	}(logger)

	sqliteStore, err := store.NewSQLite(cfg.Database)
	if err != nil {
		logger.Error("初始化数据库失败", zap.Error(err))
		os.Exit(1)
	}
	defer func() {
		if closeErr := sqliteStore.Close(); closeErr != nil {
			logger.Warn("关闭数据库失败", zap.Error(closeErr))
		}
	}()

	tradingApp := app.New(cfg, logger, sqliteStore)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := tradingApp.Run(ctx); err != nil {
		logger.Error("系统运行异常", zap.Error(err))
		os.Exit(1)
	}

	logger.Info("系统已安全退出")
}
