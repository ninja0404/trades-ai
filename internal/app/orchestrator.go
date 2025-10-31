package app

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	ccxt "github.com/ccxt/ccxt/go/v4"
	"go.uber.org/zap"

	"trades-ai/internal/ai"
	"trades-ai/internal/config"
	"trades-ai/internal/exchange"
	"trades-ai/internal/execution"
	"trades-ai/internal/feature"
	"trades-ai/internal/indicator"
	"trades-ai/internal/monitor"
	"trades-ai/internal/position"
	"trades-ai/internal/risk"
	"trades-ai/internal/store"
)

type orchestrator struct {
	market      *exchange.MarketDataService
	extractor   *feature.Extractor
	ai          *ai.Client
	risk        *risk.Manager
	executor    execution.Trader
	positionMgr *position.Manager
	monitor     *monitor.Service
	logger      *zap.Logger

	symbol           string
	decisionInterval time.Duration
	lastDecision     time.Time
}

func newOrchestrator(client *exchange.Client, cfg orchestratorConfig, logger *zap.Logger, store *store.Store) (*orchestrator, error) {
	market := exchange.NewMarketDataService(client, logger)
	indicatorCalc := indicator.NewCalculator()
	extractor := feature.NewExtractor(indicatorCalc, logger)

	aiClient, err := ai.NewClient(cfg.openAI, logger)
	if err != nil {
		return nil, fmt.Errorf("初始化AI客户端失败: %w", err)
	}

	riskMgr, err := risk.NewManager(cfg.risk, store, logger)
	if err != nil {
		return nil, fmt.Errorf("初始化风险管理失败: %w", err)
	}

	tradeClient, err := newTradeClient(cfg.trade)
	if err != nil {
		return nil, err
	}

	positionMgr := position.NewManager(tradeClient, cfg.trade.Market, logger)

	exec := execution.NewExecutor(tradeClient, cfg.trade.Market, cfg.execution.Slippage, logger)

	monitorSvc, err := monitor.NewService(store, logger)
	if err != nil {
		return nil, fmt.Errorf("初始化监控服务失败: %w", err)
	}

	interval := cfg.scheduler.DecisionInterval
	if interval <= 0 {
		interval = time.Hour
	}

	return &orchestrator{
		market:           market,
		extractor:        extractor,
		ai:               aiClient,
		risk:             riskMgr,
		executor:         exec,
		positionMgr:      positionMgr,
		monitor:          monitorSvc,
		logger:           logger,
		symbol:           cfg.trade.Market,
		decisionInterval: interval,
	}, nil
}

func (o *orchestrator) Tick(ctx context.Context) error {
	now := time.Now().UTC()
	if !o.lastDecision.IsZero() && now.Sub(o.lastDecision) < o.decisionInterval {
		return nil
	}

	snapshot, err := o.market.GetSnapshot(ctx, exchange.DefaultSnapshotRequest())
	if err != nil {
		o.monitor.RecordError(ctx, "拉取市场数据失败", err, nil)
		return err
	}

	features, err := o.extractor.Extract(ctx, snapshot)
	if err != nil {
		o.monitor.RecordError(ctx, "特征计算失败", err, nil)
		return err
	}
	o.monitor.RecordMarketSnapshot(ctx, features)

	balance, details, err := o.positionMgr.FetchSnapshot(ctx)
	if err != nil {
		o.monitor.RecordError(ctx, "获取账户仓位失败", err, nil)
		return err
	}
	o.monitor.RecordPosition(ctx, balance, details)

	price := latestPrice(snapshot)
	summary := aggregatePosition(details, balance, price)

	decision, err := o.ai.GenerateDecision(ctx, features, summary)
	if err != nil {
		o.monitor.RecordError(ctx, "AI 决策失败", err, nil)
		return err
	}
	o.monitor.RecordDecision(ctx, features, decision)

	exposurePercent := exposureFromSummary(summary)
	account := risk.AccountState{
		Equity:                 firstPositive(balance.TotalEquity, balance.TotalUSD),
		Balance:                balance.TotalUSD,
		CurrentExposurePercent: exposurePercent,
		Timestamp:              snapshot.RetrievedAt,
	}

	input := risk.EvaluationInput{
		Decision:    decision,
		Features:    features,
		Position:    summary,
		Account:     account,
		MarketPrice: price,
	}

	evaluation, err := o.risk.Evaluate(ctx, input)
	if err != nil {
		o.monitor.RecordError(ctx, "风险评估失败", err, nil)
		return err
	}
	o.monitor.RecordRisk(ctx, input, evaluation)

	if evaluation.Status != risk.StatusProceed {
		o.lastDecision = now
		return nil
	}

	side := execution.OrderSideBuy
	if evaluation.TargetExposurePercent < account.CurrentExposurePercent {
		side = execution.OrderSideSell
	}

	plan := execution.ExecutionPlan{
		Symbol:          o.symbol,
		Side:            side,
		CurrentExposure: account.CurrentExposurePercent,
		TargetExposure:  evaluation.TargetExposurePercent,
		MarketPrice:     price,
		RiskAmount:      evaluation.RiskAmount,
		Decision:        decision,
		RiskResult:      evaluation,
		Account:         balance,
		Position:        summary,
		GeneratedAt:     now,
	}

	orders, err := o.executor.BuildPlan(plan)
	if err != nil {
		o.monitor.RecordError(ctx, "生成执行计划失败", err, nil)
		return err
	}

	result, err := o.executor.Execute(ctx, orders)
	if err != nil {
		o.monitor.RecordError(ctx, "执行订单失败", err, nil)
		return err
	}
	o.monitor.RecordExecution(ctx, plan, result)

	o.lastDecision = now
	return nil
}

type orchestratorConfig struct {
	exchange  config.ExchangeConfig
	trade     config.TradeExchangeConfig
	openAI    config.OpenAIConfig
	risk      config.RiskConfig
	scheduler config.SchedulerConfig
	execution config.ExecutionConfig
}

func newTradeClient(cfg config.TradeExchangeConfig) (*ccxt.Hyperliquid, error) {
	userConfig := map[string]interface{}{
		"enableRateLimit": true,
	}
	if cfg.APIKey != "" {
		userConfig["apiKey"] = cfg.APIKey
	}
	if cfg.APISecret != "" {
		userConfig["secret"] = cfg.APISecret
	}
	if cfg.APIPass != "" {
		userConfig["password"] = cfg.APIPass
	}
	if cfg.Wallet != "" {
		userConfig["walletAddress"] = cfg.Wallet
	}
	if cfg.PrivateKey != "" {
		userConfig["privateKey"] = cfg.PrivateKey
	}
	client := ccxt.NewHyperliquid(userConfig)
	if cfg.UseSandbox {
		client.SetSandboxMode(true)
	}
	return client, nil
}

func latestPrice(snapshot exchange.MarketSnapshot) float64 {
	if len(snapshot.Candles1H) > 0 {
		return snapshot.Candles1H[len(snapshot.Candles1H)-1].Close
	}
	if len(snapshot.Candles4H) > 0 {
		return snapshot.Candles4H[len(snapshot.Candles4H)-1].Close
	}
	if len(snapshot.OrderBook.Bids) > 0 {
		return snapshot.OrderBook.Bids[0].Price
	}
	if len(snapshot.OrderBook.Asks) > 0 {
		return snapshot.OrderBook.Asks[0].Price
	}
	return 0
}

func aggregatePosition(details []position.PositionDetail, balance position.AccountBalance, price float64) position.Summary {
	if len(details) == 0 {
		return position.EmptySummary()
	}

	equity := firstPositive(balance.TotalEquity, balance.TotalUSD)
	if equity <= 0 {
		equity = 1
	}

	var totalNotional float64
	var entryWeighted float64
	var totalSize float64

	for _, d := range details {
		mark := firstPositive(d.MarkPrice, d.EntryPrice, price)
		notional := d.Size * mark
		if strings.ToUpper(d.Side) == "SHORT" {
			notional = -notional
		}
		totalNotional += notional
		entryWeighted += d.Size * d.EntryPrice
		totalSize += d.Size
	}

	side := ""
	if totalNotional > 0 {
		side = "LONG"
	} else if totalNotional < 0 {
		side = "SHORT"
	}

	entryPrice := 0.0
	if totalSize > 0 {
		entryPrice = entryWeighted / totalSize
	}

	pnlPercent := 0.0
	if entryPrice > 0 && price > 0 {
		switch side {
		case "LONG":
			pnlPercent = (price - entryPrice) / entryPrice * 100
		case "SHORT":
			pnlPercent = (entryPrice - price) / entryPrice * 100
		}
	}

	return position.Summary{
		Side:                 side,
		SizePercent:          math.Abs(totalNotional) / equity * 100,
		EntryPrice:           entryPrice,
		UnrealizedPnlPercent: pnlPercent,
		PositionAgeHours:     0,
		StopLoss:             0,
		TakeProfit:           0,
	}
}

func exposureFromSummary(summary position.Summary) float64 {
	switch strings.ToUpper(summary.Side) {
	case "LONG":
		return summary.SizePercent / 100
	case "SHORT":
		return -summary.SizePercent / 100
	default:
		return 0
	}
}

func firstPositive(values ...float64) float64 {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}
