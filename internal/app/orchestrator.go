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

type assetPipeline struct {
	assetKey       string
	exchangeSymbol string
	tradeSymbol    string
	market         *exchange.MarketDataService
	extractor      *feature.Extractor
	positionMgr    *position.Manager
	executor       execution.Trader
}

type orchestrator struct {
	assets  []assetPipeline
	ai      *ai.Client
	risk    *risk.Manager
	monitor *monitor.Service
	logger  *zap.Logger

	decisionInterval time.Duration
	lastDecision     time.Time
}

func (o *orchestrator) Monitor() *monitor.Service {
	return o.monitor
}

type orchestratorConfig struct {
	exchange  config.ExchangeConfig
	trade     config.TradeExchangeConfig
	openAI    config.OpenAIConfig
	risk      config.RiskConfig
	scheduler config.SchedulerConfig
	execution config.ExecutionConfig
}

func newOrchestrator(cfg orchestratorConfig, logger *zap.Logger, store *store.Store) (*orchestrator, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

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
		return nil, fmt.Errorf("初始化交易客户端失败: %w", err)
	}

	monitorSvc, err := monitor.NewService(store, logger)
	if err != nil {
		return nil, fmt.Errorf("初始化监控服务失败: %w", err)
	}

	if len(cfg.exchange.Markets) != len(cfg.trade.Markets) {
		return nil, fmt.Errorf("交易所与执行市场数量不一致: %d vs %d", len(cfg.exchange.Markets), len(cfg.trade.Markets))
	}

	execOpts := execution.Options{
		Slippage:    cfg.execution.Slippage,
		TimeInForce: cfg.execution.TimeInForce,
		PostOnly:    cfg.execution.PostOnly,
	}

	assets := make([]assetPipeline, 0, len(cfg.exchange.Markets))
	for i, exSymbol := range cfg.exchange.Markets {
		tradeSymbol := cfg.trade.Markets[i]
		assetKey := assetKeyFromSymbol(tradeSymbol)

		exClient, err := exchange.NewClient(cfg.exchange, exSymbol, logger)
		if err != nil {
			return nil, fmt.Errorf("初始化行情客户端失败 (%s): %w", exSymbol, err)
		}

		marketSvc := exchange.NewMarketDataService(exClient, logger)
		indicatorCalc := indicator.NewCalculator()
		extractor := feature.NewExtractor(indicatorCalc, logger)
		posMgr := position.NewManager(tradeClient, tradeSymbol, logger)

		var trader execution.Trader
		if cfg.execution.Simulation {
			logger.Info("执行器处于模拟模式", zap.String("symbol", tradeSymbol))
			trader = execution.NewSimulatedExecutor(tradeSymbol, execOpts, logger)
		} else {
			trader = execution.NewExecutor(tradeClient, tradeSymbol, execOpts, logger)
		}

		assets = append(assets, assetPipeline{
			assetKey:       assetKey,
			exchangeSymbol: exSymbol,
			tradeSymbol:    tradeSymbol,
			market:         marketSvc,
			extractor:      extractor,
			positionMgr:    posMgr,
			executor:       trader,
		})
	}

	interval := cfg.scheduler.DecisionInterval
	if interval <= 0 {
		interval = time.Hour
	}

	return &orchestrator{
		assets:           assets,
		ai:               aiClient,
		risk:             riskMgr,
		monitor:          monitorSvc,
		logger:           logger,
		decisionInterval: interval,
	}, nil
}

func (o *orchestrator) Tick(ctx context.Context) error {
	now := time.Now().UTC()
	if !o.lastDecision.IsZero() && now.Sub(o.lastDecision) < o.decisionInterval {
		return nil
	}

	assetStates := make([]assetState, 0, len(o.assets))
	promptInputs := make([]ai.AssetInput, 0, len(o.assets))

	var accountSnapshot ai.AccountSnapshot
	var accountCaptured bool
	var netExposure float64
	var grossExposure float64

	for i := range o.assets {
		asset := &o.assets[i]

		snapshot, err := asset.market.GetSnapshot(ctx, exchange.DefaultSnapshotRequest())
		if err != nil {
			o.monitor.RecordError(ctx, "拉取市场数据失败", err, map[string]interface{}{"symbol": asset.exchangeSymbol})
			return err
		}

		features, err := asset.extractor.Extract(ctx, snapshot)
		if err != nil {
			o.monitor.RecordError(ctx, "特征计算失败", err, map[string]interface{}{"symbol": asset.exchangeSymbol})
			return err
		}
		o.monitor.RecordMarketSnapshot(ctx, features)

		balance, details, err := asset.positionMgr.FetchSnapshot(ctx)
		if err != nil {
			o.monitor.RecordError(ctx, "获取账户仓位失败", err, map[string]interface{}{"symbol": asset.tradeSymbol})
			return err
		}
		o.monitor.RecordPosition(ctx, balance, details)

		price := latestPrice(snapshot)
		summary := aggregatePosition(details, balance, price)

		assetStates = append(assetStates, assetState{
			asset:    asset,
			features: features,
			balance:  balance,
			summary:  summary,
			price:    price,
		})

		promptInputs = append(promptInputs, ai.AssetInput{
			Symbol:   asset.assetKey,
			Features: features,
			Position: summary,
		})

		exposure := exposureFromSummary(summary)
		netExposure += exposure
		grossExposure += math.Abs(exposure)

		if !accountCaptured {
			accountSnapshot = ai.AccountSnapshot{
				Equity:           balance.TotalEquity,
				Balance:          balance.TotalUSD,
				FreeBalance:      balance.FreeUSD,
				Withdrawable:     balance.Withdrawable,
				MarginUsed:       balance.MarginUsed,
				TotalNotional:    balance.TotalNotional,
				UnrealizedPnL:    balance.Unrealized,
				CrossEquity:      balance.CrossEquity,
				CrossMarginUsed:  balance.CrossMarginUsed,
				NetExposurePct:   0,
				GrossExposurePct: 0,
			}
			accountCaptured = true
		}
	}

	if accountCaptured {
		accountSnapshot.NetExposurePct = netExposure * 100
		accountSnapshot.GrossExposurePct = grossExposure * 100
	}

	decisions, err := o.ai.GenerateDecision(ctx, promptInputs, accountSnapshot)
	if err != nil {
		o.monitor.RecordError(ctx, "AI 决策失败", err, nil)
		return err
	}

	decisionMap := make(map[string]ai.Decision, len(decisions))
	for _, decision := range decisions {
		key := strings.ToUpper(strings.TrimSpace(decision.Symbol))
		decisionMap[key] = decision
	}

	for _, state := range assetStates {
		assetKey := strings.ToUpper(state.asset.assetKey)
		decision, ok := decisionMap[assetKey]
		if !ok {
			o.logger.Warn("AI 未返回该资产决策", zap.String("asset", state.asset.assetKey))
			continue
		}

		o.monitor.RecordDecision(ctx, state.features, decision)

		account := risk.AccountState{
			Equity:                 firstPositive(state.balance.TotalEquity, state.balance.TotalUSD),
			Balance:                state.balance.TotalUSD,
			CurrentExposurePercent: exposureFromSummary(state.summary),
			Timestamp:              state.features.GeneratedAt,
		}

		evalInput := risk.EvaluationInput{
			Symbol:      state.asset.assetKey,
			Decision:    decision,
			Features:    state.features,
			Position:    state.summary,
			Account:     account,
			MarketPrice: state.price,
		}

		evaluation, err := o.risk.Evaluate(ctx, evalInput)
		if err != nil {
			o.monitor.RecordError(ctx, "风险评估失败", err, map[string]interface{}{"asset": state.asset.assetKey})
			return err
		}
		o.monitor.RecordRisk(ctx, evalInput, evaluation)

		if evaluation.Status != risk.StatusProceed {
			continue
		}

		side := execution.OrderSideBuy
		if evaluation.TargetExposurePercent < account.CurrentExposurePercent {
			side = execution.OrderSideSell
		}

		plan := execution.ExecutionPlan{
			Asset:           state.asset.assetKey,
			Symbol:          state.asset.tradeSymbol,
			Side:            side,
			CurrentExposure: account.CurrentExposurePercent,
			TargetExposure:  evaluation.TargetExposurePercent,
			MarketPrice:     state.price,
			StopLoss:        evaluation.RecommendedStopLoss,
			TakeProfit:      evaluation.RecommendedTakeProfit,
			RiskAmount:      evaluation.RiskAmount,
			Decision:        decision,
			RiskResult:      evaluation,
			Account:         state.balance,
			Position:        state.summary,
			GeneratedAt:     now,
		}

		orders, err := state.asset.executor.BuildPlan(plan)
		if err != nil {
			o.monitor.RecordError(ctx, "生成执行计划失败", err, map[string]interface{}{"asset": state.asset.assetKey})
			return err
		}

		result, err := state.asset.executor.Execute(ctx, orders)
		if err != nil {
			o.monitor.RecordError(ctx, "执行订单失败", err, map[string]interface{}{"asset": state.asset.assetKey})
			return err
		}
		o.monitor.RecordExecution(ctx, plan, result)
	}

	o.lastDecision = now
	return nil
}

type assetState struct {
	asset    *assetPipeline
	features feature.FeatureSet
	balance  position.AccountBalance
	summary  position.Summary
	price    float64
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

func assetKeyFromSymbol(symbol string) string {
	s := strings.TrimSpace(symbol)
	if s == "" {
		return ""
	}
	if idx := strings.Index(s, "/"); idx > 0 {
		s = s[:idx]
	}
	if idx := strings.Index(s, ":"); idx > 0 {
		s = s[:idx]
	}
	return strings.ToUpper(s)
}
