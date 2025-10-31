package execution

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	ccxt "github.com/ccxt/ccxt/go/v4"
	"go.uber.org/zap"

	"trades-ai/internal/exchange"
	"trades-ai/internal/risk"
)

type orderClient interface {
	CreateMarketOrder(symbol string, side string, amount float64, options ...ccxt.CreateMarketOrderOptions) (ccxt.Order, error)
	CreateLimitOrder(symbol string, side string, amount float64, price float64, options ...ccxt.CreateLimitOrderOptions) (ccxt.Order, error)
	CreateOrder(symbol string, typeVar string, side string, amount float64, options ...ccxt.CreateOrderOptions) (ccxt.Order, error)
}

// Executor 将风险评估转化为具体下单操作。
type Executor struct {
	client   orderClient
	symbol   string
	logger   *zap.Logger
	maxRetry int
	slippage float64
}

// NewExecutor 创建执行器。
func NewExecutor(client orderClient, symbol string, slippage float64, logger *zap.Logger) *Executor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Executor{
		client:   client,
		symbol:   symbol,
		logger:   logger,
		maxRetry: 3,
		slippage: slippage,
	}
}

// BuildPlan 根据风险评估生成执行计划。
func (e *Executor) BuildPlan(plan ExecutionPlan) ([]OrderRequest, error) {
	return buildOrderRequests(plan, e.slippage)
}

// Execute 提交订单并处理异常。
func (e *Executor) Execute(ctx context.Context, orders []OrderRequest) (Result, error) {
	result := Result{
		Orders:        orders,
		Executed:      false,
		ExecutionTime: time.Now().UTC(),
		Notes:         make([]string, 0),
	}

	if len(orders) == 0 {
		return result, nil
	}

	for _, order := range orders {
		if err := e.submitOrder(ctx, order); err != nil {
			result.Notes = append(result.Notes, fmt.Sprintf("下单失败: %v", err))
			return result, err
		}
	}

	result.Executed = true
	return result, nil
}

func (e *Executor) submitOrder(ctx context.Context, order OrderRequest) error {
	// 验证订单数量
	if order.Amount <= 0 {
		return fmt.Errorf("execution: 订单数量无效 amount=%.8f type=%s side=%s",
			order.Amount, order.Type, order.Side)
	}

	e.logger.Info("提交订单",
		zap.String("type", order.Type),
		zap.String("side", string(order.Side)),
		zap.Float64("amount", order.Amount),
		zap.Float64("price", order.Price),
		zap.Bool("reduceOnly", order.ReduceOnly),
		zap.Bool("closeAll", order.CloseAll),
		zap.Bool("isTrigger", order.IsTrigger),
	)

	var err error
	for attempt := 1; attempt <= e.maxRetry; attempt++ {
		params := cloneParams(order.Params)
		if order.IsTrigger {
			var opts []ccxt.CreateOrderOptions
			if order.Price > 0 {
				opts = append(opts, ccxt.WithCreateOrderPrice(order.Price))
			}
			if len(params) > 0 {
				opts = append(opts, ccxt.WithCreateOrderParams(params))
			}
			_, err = e.client.CreateOrder(e.symbol, order.Type, string(order.Side), order.Amount, opts...)
		} else {
			switch order.Type {
			case "market":
				var opts []ccxt.CreateMarketOrderOptions
				// Hyperliquid 需要价格来计算最大滑点价格
				if order.Price > 0 {
					opts = append(opts, ccxt.WithCreateMarketOrderPrice(order.Price))
				}
				if len(params) > 0 {
					opts = append(opts, ccxt.WithCreateMarketOrderParams(params))
				}
				_, err = e.client.CreateMarketOrder(e.symbol, string(order.Side), order.Amount, opts...)
			case "limit":
				var opts []ccxt.CreateLimitOrderOptions
				if len(params) > 0 {
					opts = append(opts, ccxt.WithCreateLimitOrderParams(params))
				}
				_, err = e.client.CreateLimitOrder(e.symbol, string(order.Side), order.Amount, order.Price, opts...)
			default:
				return fmt.Errorf("execution: 不支持的订单类型 %s", order.Type)
			}
		}

		if err == nil {
			return nil
		}

		if !exchangeRetryable(err) {
			return err
		}

		wait := time.Duration(attempt) * time.Second
		e.logger.Warn("下单失败，准备重试",
			zap.Int("attempt", attempt),
			zap.Duration("wait", wait),
			zap.Error(err),
		)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}

	return fmt.Errorf("execution: 重试后仍下单失败: %w", err)
}

func oppositeSide(side OrderSide) OrderSide {
	if side == OrderSideBuy {
		return OrderSideSell
	}
	return OrderSideBuy
}

func exchangeRetryable(err error) bool {
	return exchange.IsRetryable(err)
}

func sameDirection(a, b float64) bool {
	if a == 0 || b == 0 {
		return false
	}
	return (a > 0 && b > 0) || (a < 0 && b < 0)
}

func cloneParams(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func parsePriceString(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil || f <= 0 {
		return 0, false
	}
	return f, true
}

func formatPrice(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func formatSlippage(value float64) string {
	return fmt.Sprintf("%.6f", value)
}

func buildOrderRequests(plan ExecutionPlan, slippage float64) ([]OrderRequest, error) {
	if plan.RiskResult.Status != risk.StatusProceed {
		return nil, errors.New("execution: 风控未允许执行")
	}

	if plan.MarketPrice <= 0 {
		return nil, errors.New("execution: 市场价格无效")
	}

	target := plan.TargetExposure
	current := plan.CurrentExposure
	exposureDiff := target - current
	if math.Abs(exposureDiff) < 1e-6 {
		return nil, errors.New("execution: 目标仓位与当前仓位一致，无需执行")
	}

	side := OrderSideBuy
	if exposureDiff < 0 {
		side = OrderSideSell
	}

	amountExposure := math.Abs(exposureDiff)
	if plan.Account.TotalEquity <= 0 {
		return nil, errors.New("execution: 账户净值无效")
	}
	amount := amountExposure * plan.Account.TotalEquity / plan.MarketPrice
	if amount <= 0 {
		return nil, fmt.Errorf("execution: 计算下单手数无效 amount=%.8f exposureDiff=%.6f equity=%.2f price=%.2f",
			amount, exposureDiff, plan.Account.TotalEquity, plan.MarketPrice)
	}

	reduceOnly := sameDirection(target, current) && math.Abs(target) <= math.Abs(current)
	closeAll := math.Abs(target) < 1e-6
	if closeAll {
		reduceOnly = true
	}

	params := map[string]interface{}{
		"reduceOnly": reduceOnly,
	}
	if closeAll {
		params["closePosition"] = true
	}
	if slippage > 0 {
		params["slippage"] = formatSlippage(slippage)
	}

	orders := make([]OrderRequest, 0, 3)
	orders = append(orders, OrderRequest{
		Type:       "market",
		Side:       side,
		Amount:     amount,
		Price:      plan.MarketPrice,
		ReduceOnly: reduceOnly,
		CloseAll:   closeAll,
		Params:     params,
	})

	// protective orders (stop-loss / take-profit)
	targetSign := 0
	if target > 0 {
		targetSign = 1
	} else if target < 0 {
		targetSign = -1
	}

	stopLossPrice, hasSL := parsePriceString(plan.Decision.NewStopLoss)
	takeProfitPrice, hasTP := parsePriceString(plan.Decision.NewTakeProfit)

	if targetSign != 0 && !closeAll && plan.Account.TotalEquity > 0 {
		protectionAmount := math.Abs(target) * plan.Account.TotalEquity / plan.MarketPrice
		if protectionAmount > 0 {
			triggerSide := OrderSideSell
			if targetSign < 0 {
				triggerSide = OrderSideBuy
			}

			baseParams := map[string]interface{}{
				"reduceOnly": true,
			}
			if slippage > 0 {
				baseParams["slippage"] = formatSlippage(slippage)
			}

			if hasTP {
				params := cloneParams(baseParams)
				params["takeProfitPrice"] = formatPrice(takeProfitPrice)
				orders = append(orders, OrderRequest{
					Type:         "limit",
					Side:         triggerSide,
					Amount:       protectionAmount,
					Price:        takeProfitPrice,
					ReduceOnly:   true,
					Params:       params,
					IsTrigger:    true,
					TriggerType:  "take_profit",
					TriggerPrice: takeProfitPrice,
				})
			}

			if hasSL {
				params := cloneParams(baseParams)
				params["stopLossPrice"] = formatPrice(stopLossPrice)
				orders = append(orders, OrderRequest{
					Type:         "limit",
					Side:         triggerSide,
					Amount:       protectionAmount,
					Price:        stopLossPrice,
					ReduceOnly:   true,
					Params:       params,
					IsTrigger:    true,
					TriggerType:  "stop_loss",
					TriggerPrice: stopLossPrice,
				})
			}
		}
	}

	return orders, nil
}
