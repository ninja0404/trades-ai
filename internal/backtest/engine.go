package backtest

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"trades-ai/internal/feature"
	"trades-ai/internal/risk"
)

// Result 汇总回测结果。
type Result struct {
	Metrics      Metrics
	EquityCurve  []float64
	ReturnSeries []float64
	Trades       int
	FinalEquity  float64
}

// Engine 串联数据源、AI、风控与模拟执行。
type Engine struct {
	cfg       Config
	provider  SnapshotProvider
	extractor *feature.Extractor
	decision  DecisionProvider
	risk      *risk.Manager
	simulator *Simulator
	logger    *zap.Logger
}

// NewEngine 构建回测引擎。
func NewEngine(cfg Config, provider SnapshotProvider, extractor *feature.Extractor, decision DecisionProvider, riskMgr *risk.Manager, logger *zap.Logger) (*Engine, error) {
	if provider == nil {
		return nil, fmt.Errorf("backtest: provider 不能为空")
	}
	if extractor == nil {
		return nil, fmt.Errorf("backtest: feature extractor 不能为空")
	}
	if decision == nil {
		return nil, fmt.Errorf("backtest: decision provider 不能为空")
	}
	if riskMgr == nil {
		return nil, fmt.Errorf("backtest: risk manager 不能为空")
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	cfg = cfg.normalize()
	simulator := NewSimulator(cfg.InitialEquity)

	return &Engine{
		cfg:       cfg,
		provider:  provider,
		extractor: extractor,
		decision:  decision,
		risk:      riskMgr,
		simulator: simulator,
		logger:    logger,
	}, nil
}

// Run 执行完整回测流程。
func (e *Engine) Run(ctx context.Context) (Result, error) {
	var price float64
	for {
		snapshot, ok, err := e.provider.Next(ctx)
		if err != nil {
			return Result{}, err
		}
		if !ok {
			break
		}

		price = snapshot.Candles1H[len(snapshot.Candles1H)-1].Close
		e.simulator.Advance(price)

		features, err := e.extractor.Extract(ctx, snapshot)
		if err != nil {
			e.logger.Warn("计算特征失败", zap.Error(err))
			continue
		}

		summary := e.simulator.Summary(price, snapshot.RetrievedAt)
		decision, err := e.decision.Decide(ctx, features, summary)
		if err != nil {
			e.logger.Warn("获取决策失败", zap.Error(err))
			continue
		}

		account := risk.AccountState{
			Equity:                 e.simulator.Equity(),
			Balance:                e.simulator.Equity(),
			CurrentExposurePercent: e.simulator.Exposure(),
			Timestamp:              snapshot.RetrievedAt,
		}
		input := risk.EvaluationInput{
			Decision:    decision,
			Features:    features,
			Position:    summary,
			Account:     account,
			MarketPrice: price,
		}

		evaluation, err := e.risk.Evaluate(ctx, input)
		if err != nil {
			e.logger.Warn("风控评估失败", zap.Error(err))
			continue
		}

		if evaluation.Status != risk.StatusProceed {
			continue
		}

		e.simulator.AdjustExposure(evaluation.TargetExposurePercent, price, snapshot.RetrievedAt)
	}

	metrics := calculateMetrics(e.simulator.EquityHistory(), e.simulator.ReturnHistory())
	return Result{
		Metrics:      metrics,
		EquityCurve:  e.simulator.EquityHistory(),
		ReturnSeries: e.simulator.ReturnHistory(),
		Trades:       e.simulator.TradeCount(),
		FinalEquity:  e.simulator.Equity(),
	}, nil
}
