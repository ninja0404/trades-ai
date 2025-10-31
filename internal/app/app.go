package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"trades-ai/internal/config"
	"trades-ai/internal/exchange"
	"trades-ai/internal/store"
)

// App 聚合核心依赖并驱动系统生命周期。
type App struct {
	cfg    *config.Config
	logger *zap.Logger
	store  *store.Store
}

// New 创建 App 实例。
func New(cfg *config.Config, logger *zap.Logger, store *store.Store) *App {
	return &App{
		cfg:    cfg,
		logger: logger,
		store:  store,
	}
}

// Run 暂时只阻塞等待退出信号，后续将在此驱动主业务循环。
func (a *App) Run(ctx context.Context) error {
	a.logger.Info("交易系统已初始化",
		zap.String("environment", a.cfg.App.Environment),
		zap.String("exchange", a.cfg.Exchange.Name),
		zap.String("market", a.cfg.Exchange.Market),
	)

	exClient, err := exchange.NewClient(a.cfg.Exchange, a.logger)
	if err != nil {
		return fmt.Errorf("初始化交易所客户端失败: %w", err)
	}

	orchestrator, err := newOrchestrator(exClient, orchestratorConfig{
		exchange:  a.cfg.Exchange,
		trade:     a.cfg.Trade,
		openAI:    a.cfg.OpenAI,
		risk:      a.cfg.Risk,
		scheduler: a.cfg.Scheduler,
		execution: a.cfg.Execution,
	}, a.logger, a.store)
	if err != nil {
		return err
	}

	loopInterval := a.cfg.Scheduler.LoopInterval
	if loopInterval <= 0 {
		loopInterval = 5 * time.Minute
	}

	if err := orchestrator.Tick(ctx); err != nil {
		a.logger.Error("首次执行失败", zap.Error(err))
	}

	ticker := time.NewTicker(loopInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("系统异常退出: %w", err)
			}
			a.logger.Info("系统收到退出信号，正在停止")
			return nil
		case <-ticker.C:
			if err := orchestrator.Tick(ctx); err != nil {
				a.logger.Error("执行调度失败", zap.Error(err))
			}
		}
	}
}
