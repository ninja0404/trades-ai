package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"trades-ai/internal/config"
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
		zap.Strings("markets", a.cfg.Exchange.Markets),
	)

	orch, err := newOrchestrator(orchestratorConfig{
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

	if err = orch.Tick(ctx); err != nil {
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
			if err = orch.Tick(ctx); err != nil {
				a.logger.Error("执行调度失败", zap.Error(err))
			}
		}
	}
}
