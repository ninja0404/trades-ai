//go:build integration
// +build integration

package execution

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	ccxt "github.com/ccxt/ccxt/go/v4"
	"go.uber.org/zap"

	"trades-ai/internal/ai"
	"trades-ai/internal/config"
	"trades-ai/internal/exchange"
	"trades-ai/internal/position"
	"trades-ai/internal/risk"
)

func TestExecutorIntegration_HyperliquidSubmit(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("integration test panic: %v", r)
		}
	}()

	configPath := os.Getenv("TRADES_CONFIG")
	if configPath == "" {
		configPath = "../../configs/config.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	if !cfg.Trade.UseSandbox {
		t.Skip("trade_exchange.use_sandbox=false，出于安全考虑跳过真实下单测试")
	}
	if len(cfg.Trade.Markets) == 0 || len(cfg.Exchange.Markets) == 0 {
		t.Skip("配置缺少交易标的，跳过测试")
	}
	if cfg.Trade.Wallet == "" || cfg.Trade.PrivateKey == "" {
		t.Skip("缺少 Hyperliquid 钱包配置，跳过测试")
	}

	assetIndex := 0
	tradeSymbol := cfg.Trade.Markets[assetIndex]
	exSymbol := cfg.Exchange.Markets[assetIndex]
	assetKey := integrationAssetKey(tradeSymbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 行情客户端用于获取最新价格
	exClient, err := exchange.NewClient(cfg.Exchange, exSymbol, zap.NewNop())
	if err != nil {
		t.Fatalf("初始化行情客户端失败: %v", err)
	}
	marketSvc := exchange.NewMarketDataService(exClient, zap.NewNop())
	snapshot, err := marketSvc.GetSnapshot(ctx, exchange.DefaultSnapshotRequest())
	if err != nil {
		t.Fatalf("获取市场快照失败: %v", err)
	}
	price := latestPriceFromSnapshot(snapshot)
	if price <= 0 {
		t.Fatalf("无法解析有效市场价格")
	}

	// 交易客户端
	tradeClient, err := newIntegrationTradeClient(cfg)
	if err != nil {
		t.Fatalf("初始化 Hyperliquid 客户端失败: %v", err)
	}

	// 获取账户与持仓
	posMgr := position.NewManager(tradeClient, tradeSymbol, zap.NewNop())
	balance, positions, err := posMgr.FetchSnapshot(ctx)
	if err != nil {
		t.Fatalf("获取账户仓位失败: %v", err)
	}
	if balance.TotalEquity <= 0 {
		t.Skip("账户净值为 0，跳过真实下单测试")
	}

	currentExposure := currentExposurePercent(positions, balance.TotalEquity, tradeSymbol)
	// 目标仓位：在现有基础上调整 0.05%
	minAmount, err := fetchMinAmount(tradeClient, tradeSymbol)
	if err != nil {
		t.Fatalf("获取最小下单量失败: %v", err)
	}
	if minAmount <= 0 {
		minAmount = 0.001
	}

	desiredNotionalUSD := 200.0
	maxNotionalByExposure := balance.TotalEquity * cfg.Risk.MaxExposure * 0.25
	if maxNotionalByExposure <= 0 {
		t.Skip("风控上限不足以进行测试")
	}
	if desiredNotionalUSD > maxNotionalByExposure {
		desiredNotionalUSD = maxNotionalByExposure
	}
	if desiredNotionalUSD < minAmount*price {
		desiredNotionalUSD = minAmount * price * 1.2
	}

	if desiredNotionalUSD < minAmount*price {
		t.Skipf("账户净值 %.2f 不足以满足最小下单量 %.6f", balance.TotalEquity, minAmount)
	}

	desiredAmount := desiredNotionalUSD / price
	if desiredAmount < minAmount {
		desiredAmount = minAmount
	}

	exposureDiff := desiredAmount * price / balance.TotalEquity
	targetExposure := currentExposure + exposureDiff

	stop := price * 0.98
	tp := price * 1.02

	plan := ExecutionPlan{
		Asset:           assetKey,
		Symbol:          tradeSymbol,
		CurrentExposure: currentExposure,
		TargetExposure:  targetExposure,
		MarketPrice:     price,
		RiskAmount:      balance.TotalEquity * 0.001,
		Decision: ai.Decision{
			Symbol:            assetKey,
			Intent:            "OPEN",
			Direction:         "LONG",
			TargetExposurePct: math.Abs(targetExposure),
			AdjustmentPct:     0,
			Confidence:        0.8,
			Reasoning:         "integration test",
			OrderPreference:   "MARKET",
			NewStopLoss:       fmt.Sprintf("%.2f", stop),
			NewTakeProfit:     fmt.Sprintf("%.2f", tp),
		},
		RiskResult: risk.EvaluationResult{
			Status:                risk.StatusProceed,
			TargetExposurePercent: targetExposure,
			RiskAmount:            balance.TotalEquity * 0.001,
		},
		Account: balance,
	}

	executor := NewExecutor(tradeClient, tradeSymbol, cfg.Execution.Slippage, zap.NewNop())
	orders, err := executor.BuildPlan(plan)
	if err != nil {
		t.Fatalf("BuildPlan 失败: %v", err)
	}
	if len(orders) == 0 {
		t.Fatalf("BuildPlan 未生成任何订单")
	}

	result, err := executeSafely(ctx, executor, orders)
	if err != nil {
		t.Fatalf("Execute 下单失败: %v", err)
	}
	if !result.Executed {
		t.Fatalf("Execute 返回未执行")
	}

	t.Logf("成功提交 %d 笔订单，asset=%s symbol=%s target=%.5f current=%.5f", len(result.Orders), assetKey, tradeSymbol, targetExposure, currentExposure)
}

func newIntegrationTradeClient(cfg config.Config) (*ccxt.Hyperliquid, error) {
	userConfig := map[string]interface{}{
		"enableRateLimit": true,
	}
	if cfg.Trade.APIKey != "" {
		userConfig["apiKey"] = cfg.Trade.APIKey
	}
	if cfg.Trade.APISecret != "" {
		userConfig["secret"] = cfg.Trade.APISecret
	}
	if cfg.Trade.APIPass != "" {
		userConfig["password"] = cfg.Trade.APIPass
	}
	if cfg.Trade.Wallet != "" {
		userConfig["walletAddress"] = cfg.Trade.Wallet
	}
	if cfg.Trade.PrivateKey != "" {
		userConfig["privateKey"] = cfg.Trade.PrivateKey
	}
	client := ccxt.NewHyperliquid(userConfig)
	if cfg.Trade.UseSandbox {
		client.SetSandboxMode(true)
	}
	return client, nil
}

func currentExposurePercent(details []position.PositionDetail, equity float64, symbol string) float64 {
	if equity <= 0 {
		return 0
	}
	var exposure float64
	for _, d := range details {
		if !strings.EqualFold(d.Symbol, symbol) {
			continue
		}
		value := d.PositionValue
		if value == 0 {
			value = d.Notional
		}
		if d.Side == "SHORT" {
			exposure -= value / equity
		} else {
			exposure += value / equity
		}
	}
	return exposure
}

func integrationAssetKey(symbol string) string {
	s := strings.TrimSpace(symbol)
	if idx := strings.Index(s, "/"); idx > 0 {
		s = s[:idx]
	}
	if idx := strings.Index(s, ":"); idx > 0 {
		s = s[:idx]
	}
	return strings.ToUpper(s)
}

func latestPriceFromSnapshot(snapshot exchange.MarketSnapshot) float64 {
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

func executeSafely(ctx context.Context, exec *Executor, orders []OrderRequest) (Result, error) {
	var (
		res Result
		err error
	)
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic during order submission: %v", r)
			res = Result{}
		}
	}()
	res, err = exec.Execute(ctx, orders)
	return res, err
}

func fetchMinAmount(client *ccxt.Hyperliquid, symbol string) (float64, error) {
	if _, err := client.LoadMarkets(); err != nil {
		return 0, err
	}
	market := client.Market(symbol)
	marketMap, ok := market.(map[string]interface{})
	if !ok {
		return 0, nil
	}
	limits, _ := marketMap["limits"].(map[string]interface{})
	if limits == nil {
		return 0, nil
	}
	amount, _ := limits["amount"].(map[string]interface{})
	if amount == nil {
		return 0, nil
	}
	if minVal, ok := amount["min"].(float64); ok {
		return minVal, nil
	}
	return 0, nil
}
