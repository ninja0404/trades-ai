//go:build integration
// +build integration

package execution

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
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
	assetKey := integrationAssetKey(tradeSymbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 交易客户端
	tradeClient, err := newIntegrationTradeClient(cfg)
	if err != nil {
		t.Fatalf("初始化 Hyperliquid 客户端失败: %v", err)
	}

	// 从 Hyperliquid 获取最新价格（使用标记价格）
	if _, err := tradeClient.LoadMarkets(); err != nil {
		t.Fatalf("加载市场信息失败: %v", err)
	}
	market := tradeClient.Market(tradeSymbol)
	marketMap, ok := market.(map[string]interface{})
	if !ok {
		t.Fatalf("无法解析市场信息")
	}
	info, ok := marketMap["info"].(map[string]interface{})
	if !ok {
		t.Fatalf("无法获取市场 info")
	}

	// 使用 markPx 作为参考价格
	price := 0.0
	if markPx, ok := info["markPx"].(float64); ok && markPx > 0 {
		price = markPx
	} else if markPxStr, ok := info["markPx"].(string); ok {
		// 尝试解析字符串格式的价格
		if parsedPrice, err := strconv.ParseFloat(markPxStr, 64); err == nil && parsedPrice > 0 {
			price = parsedPrice
		}
	}
	if price <= 0 {
		t.Fatalf("无法获取有效的标记价格，info: %+v", info)
	}
	fmt.Printf("[价格信息] 使用 Hyperliquid 标记价格: %.2f\n", price)

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
	// 获取市场约束
	minAmount, minCost, amountDecimals, err := fetchMarketConstraints(tradeClient, tradeSymbol)
	if err != nil {
		t.Fatalf("获取市场约束失败: %v", err)
	}

	// 根据最小金额和当前价格计算最小下单数量
	minAmountByPrice := minCost / price

	// 如果没有获取到最小下单量，使用基于价格计算的值
	if minAmount <= 0 {
		minAmount = minAmountByPrice
	} else if minAmountByPrice > minAmount {
		minAmount = minAmountByPrice
	}

	// 计算期望的下单名义价值
	desiredNotionalUSD := 5000.0
	maxNotionalByExposure := balance.TotalEquity * cfg.Risk.MaxExposure * 0.25
	if maxNotionalByExposure <= 0 {
		t.Skip("风控上限不足以进行测试")
	}
	if desiredNotionalUSD > maxNotionalByExposure {
		desiredNotionalUSD = maxNotionalByExposure
	}

	// 确保名义价值至少满足最小下单金额
	if desiredNotionalUSD < minCost*1.5 {
		desiredNotionalUSD = minCost * 1.5 // 留50%余量
	}

	// 计算下单数量
	desiredAmount := desiredNotionalUSD / price

	// 量化数量 - Hyperliquid 的 precision.amount=1 可能表示只支持整数
	// 先尝试量化
	desiredAmount = quantizeAmount(desiredAmount, amountDecimals)

	// 如果量化后为0或小于最小值，使用最小下单量
	if desiredAmount <= 0 || desiredAmount < minAmount {
		desiredAmount = minAmount
		// 再次量化确保符合精度要求
		desiredAmount = quantizeAmount(desiredAmount, amountDecimals)
	}

	// 如果量化后仍然为0，使用整数1
	if desiredAmount <= 0 {
		desiredAmount = 1.0
	}

	// 如果 precision.amount=1 且数量小于1，强制使用1
	// 这是因为 Hyperliquid 可能要求整数合约数量
	if amountDecimals == 1 && desiredAmount < 1.0 {
		desiredAmount = 1.0
		fmt.Printf("[警告] precision.amount=1 可能要求整数，将数量调整为 1.0\n")
	}

	// 最终验证
	if desiredAmount <= 0 {
		t.Skipf("无法计算有效下单数量，decimals=%d minAmount=%.8f minCost=%.2f price=%.2f",
			amountDecimals, minAmount, minCost, price)
	}

	actualNotional := desiredAmount * price
	if actualNotional < minCost {
		t.Skipf("计算的名义价值 %.2f 小于最小下单金额 %.2f", actualNotional, minCost)
	}

	fmt.Printf("[下单参数] price=%.2f desiredAmount=%.8f minAmount=%.8f minCost=%.2f notional=%.2f\n",
		price, desiredAmount, minAmount, minCost, actualNotional)

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

	// 打印订单详情
	for i, order := range orders {
		fmt.Printf("[订单 %d] type=%s side=%s amount=%.8f price=%.2f reduceOnly=%v closeAll=%v params=%+v\n",
			i+1, order.Type, order.Side, order.Amount, order.Price, order.ReduceOnly, order.CloseAll, order.Params)
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
		"options": map[string]interface{}{
			"defaultType": "swap", // 设置默认交易类型为永续合约
		},
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

func fetchMarketConstraints(client *ccxt.Hyperliquid, symbol string) (minAmount float64, minCost float64, amountDecimals int, err error) {
	if _, err := client.LoadMarkets(); err != nil {
		return 0, 0, 0, err
	}
	market := client.Market(symbol)
	marketMap, ok := market.(map[string]interface{})
	if !ok {
		return 0, 0, 0, fmt.Errorf("无法解析市场信息为 map")
	}

	// 打印市场约束信息用于调试
	fmt.Printf("[市场信息] symbol=%s\n", symbol)

	// 获取最小下单量和最小金额
	minCost = 10.0 // Hyperliquid 默认最小下单金额
	limits, _ := marketMap["limits"].(map[string]interface{})
	if limits != nil {
		if amount, ok := limits["amount"].(map[string]interface{}); ok {
			if minVal, ok := amount["min"].(float64); ok {
				minAmount = minVal
			}
		}
		if cost, ok := limits["cost"].(map[string]interface{}); ok {
			if minVal, ok := cost["min"].(float64); ok && minVal > 0 {
				minCost = minVal
			}
		}
	}

	// 获取数量精度（小数位数）
	amountDecimals = 8 // 默认8位小数
	if precMap, ok := marketMap["precision"].(map[string]interface{}); ok {
		if amtPrec, ok := precMap["amount"].(float64); ok {
			amountDecimals = int(amtPrec)
		}
	}

	fmt.Printf("[市场约束] symbol=%s minAmount=%.8f minCost=%.2f amountDecimals=%d\n", symbol, minAmount, minCost, amountDecimals)
	return minAmount, minCost, amountDecimals, nil
}

// quantizeAmount 将数量按照小数位数进行量化
// decimals: 小数位数，例如 decimals=3 表示保留3位小数
func quantizeAmount(amount float64, decimals int) float64 {
	if decimals < 0 {
		decimals = 0
	}
	if amount <= 0 {
		return 0
	}

	factor := math.Pow10(decimals)
	quantized := math.Floor(amount*factor+1e-9) / factor

	fmt.Printf("[数量量化] 原始=%.8f decimals=%d 量化后=%.8f\n", amount, decimals, quantized)
	return quantized
}
