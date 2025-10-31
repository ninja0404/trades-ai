package execution

import (
	"context"
	"errors"
	"fmt"
	"math"
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

// Options 控制下单参数。
type Options struct {
	Slippage    float64
	TimeInForce string
	PostOnly    bool
}

// Executor 将风险评估转化为具体下单操作。
type Executor struct {
	client   orderClient
	symbol   string
	logger   *zap.Logger
	maxRetry int
	opts     Options
}

// NewExecutor 创建执行器。
func NewExecutor(client orderClient, symbol string, opts Options, logger *zap.Logger) *Executor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Executor{
		client:   client,
		symbol:   symbol,
		logger:   logger,
		maxRetry: 3,
		opts:     opts,
	}
}

// BuildPlan 根据风险评估生成执行计划。
func (e *Executor) BuildPlan(plan ExecutionPlan) ([]OrderRequest, error) {
	return buildOrderRequests(plan, e.opts)
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
	var err error
	for attempt := 1; attempt <= e.maxRetry; attempt++ {
		params := order.Params
		switch order.Type {
		case "market":
			var opts []ccxt.CreateMarketOrderOptions
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

func formatSlippage(value float64) string {
	return fmt.Sprintf("%.6f", value)
}

func buildOrderRequests(plan ExecutionPlan, opts Options) ([]OrderRequest, error) {
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

	if plan.Account.TotalEquity <= 0 {
		return nil, errors.New("execution: 账户净值无效")
	}
	amount := math.Abs(exposureDiff) * plan.Account.TotalEquity / plan.MarketPrice
	if amount <= 0 {
		return nil, fmt.Errorf("execution: 计算下单手数无效 amount=%.6f", amount)
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
	if opts.Slippage > 0 {
		params["slippage"] = formatSlippage(opts.Slippage)
	}
	if opts.PostOnly {
		params["postOnly"] = true
	}
	if opts.TimeInForce != "" {
		params["timeInForce"] = strings.ToLower(opts.TimeInForce)
	}
	if !closeAll {
		if plan.StopLoss > 0 {
			params["stopLossPrice"] = plan.StopLoss
		}
		if plan.TakeProfit > 0 {
			params["takeProfitPrice"] = plan.TakeProfit
		}
	}

	order := OrderRequest{
		Type:       "market",
		Side:       side,
		Amount:     amount,
		Price:      plan.MarketPrice,
		ReduceOnly: reduceOnly,
		CloseAll:   closeAll,
		Params:     params,
		IsTrigger:  (!closeAll && (plan.StopLoss > 0 || plan.TakeProfit > 0)),
	}

	return []OrderRequest{order}, nil
}
