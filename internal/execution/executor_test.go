package execution

import (
	"context"
	"strings"
	"testing"

	ccxt "github.com/ccxt/ccxt/go/v4"

	"trades-ai/internal/ai"
	"trades-ai/internal/position"
	"trades-ai/internal/risk"
)

func TestBuildOrderRequests_IncludesProtectionParams(t *testing.T) {
	plan := makeBasePlan()
	plan.CurrentExposure = 0
	plan.TargetExposure = 0.10
	plan.MarketPrice = 50000
	plan.Account.TotalEquity = 100000
	plan.StopLoss = 45000
	plan.TakeProfit = 52000

	orders, err := buildOrderRequests(plan, Options{Slippage: 0.01, TimeInForce: "GTC"})
	if err != nil {
		t.Fatalf("buildOrderRequests returned error: %v", err)
	}

	if len(orders) != 1 {
		t.Fatalf("expected single combined order, got %d", len(orders))
	}

	main := orders[0]
	if main.Type != "market" {
		t.Errorf("expected main order type 'market', got %s", main.Type)
	}
	if main.Side != OrderSideBuy {
		t.Errorf("expected main order side buy, got %s", main.Side)
	}
	expectedAmount := 0.10 * plan.Account.TotalEquity / plan.MarketPrice
	if diff := abs(main.Amount - expectedAmount); diff > 1e-6 {
		t.Errorf("unexpected main order amount, diff=%f", diff)
	}
	if main.ReduceOnly {
		t.Errorf("expected main order reduceOnly=false")
	}
	if val, ok := main.Params["slippage"]; !ok || val != formatSlippage(0.01) {
		t.Errorf("expected slippage param, got %v", main.Params)
	}
	if main.Params["stopLossPrice"] != float64(45000) {
		t.Errorf("expected stopLossPrice=45000, got %v", main.Params["stopLossPrice"])
	}
	if main.Params["takeProfitPrice"] != float64(52000) {
		t.Errorf("expected takeProfitPrice=52000, got %v", main.Params["takeProfitPrice"])
	}
	if tif := main.Params["timeInForce"]; tif != "gtc" {
		t.Errorf("expected timeInForce=gtc, got %v", tif)
	}
	if !main.IsTrigger {
		t.Errorf("expected IsTrigger=true when stop/take present")
	}
}

func TestExecutorExecute_SubmitsOrdersInSequence(t *testing.T) {
	plan := makeBasePlan()
	plan.CurrentExposure = 0
	plan.TargetExposure = 0.10
	plan.MarketPrice = 50000
	plan.Account.TotalEquity = 100000
	plan.StopLoss = 45000
	plan.TakeProfit = 52000

	orders, err := buildOrderRequests(plan, Options{Slippage: 0.01, TimeInForce: "IOC"})
	if err != nil {
		t.Fatalf("buildOrderRequests returned error: %v", err)
	}

	mockClient := &mockOrderClient{}
	exec := NewExecutor(mockClient, plan.Symbol, Options{Slippage: 0.01, TimeInForce: "IOC"}, nil)
	result, err := exec.Execute(context.Background(), orders)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Executed {
		t.Fatalf("expected result.Executed=true")
	}

	expected := []string{"CreateMarketOrder"}
	if len(mockClient.calls) != len(expected) {
		t.Fatalf("unexpected call count: got %d want %d", len(mockClient.calls), len(expected))
	}
	for i, call := range expected {
		if mockClient.calls[i] != call {
			t.Errorf("call %d mismatch: got %s want %s", i, mockClient.calls[i], call)
		}
	}
}

func TestBuildOrderRequests_Errors(t *testing.T) {
	plan := makeBasePlan()
	plan.RiskResult.Status = risk.StatusDeny

	if _, err := buildOrderRequests(plan, Options{Slippage: 0.01}); err == nil || !strings.Contains(err.Error(), "风控未允许") {
		t.Fatalf("expected risk denial error, got %v", err)
	}

	plan.RiskResult.Status = risk.StatusProceed
	plan.TargetExposure = plan.CurrentExposure
	plan.MarketPrice = 50000
	plan.Account.TotalEquity = 100000

	if _, err := buildOrderRequests(plan, Options{Slippage: 0.01}); err == nil || !strings.Contains(err.Error(), "目标仓位与当前仓位一致") {
		t.Fatalf("expected no-op error, got %v", err)
	}
}

func makeBasePlan() ExecutionPlan {
	return ExecutionPlan{
		Asset:           "BTC",
		Symbol:          "BTC/USDC",
		CurrentExposure: 0,
		TargetExposure:  0.05,
		MarketPrice:     50000,
		StopLoss:        0,
		TakeProfit:      0,
		Account: position.AccountBalance{
			TotalEquity: 100000,
			TotalUSD:    100000,
		},
		Decision: ai.Decision{
			Symbol:          "BTC",
			Intent:          "OPEN",
			Direction:       "LONG",
			Confidence:      0.9,
			Reasoning:       "test",
			OrderPreference: "MARKET",
		},
		RiskResult: risk.EvaluationResult{
			Status:     risk.StatusProceed,
			RiskAmount: 1000,
		},
		Position: position.Summary{},
	}
}

type mockOrderClient struct {
	calls []string
}

func (m *mockOrderClient) CreateMarketOrder(symbol string, side string, amount float64, options ...ccxt.CreateMarketOrderOptions) (ccxt.Order, error) {
	m.calls = append(m.calls, "CreateMarketOrder")
	return ccxt.Order{}, nil
}

func (m *mockOrderClient) CreateLimitOrder(symbol string, side string, amount float64, price float64, options ...ccxt.CreateLimitOrderOptions) (ccxt.Order, error) {
	m.calls = append(m.calls, "CreateLimitOrder")
	return ccxt.Order{}, nil
}

func (m *mockOrderClient) CreateOrder(symbol string, typeVar string, side string, amount float64, options ...ccxt.CreateOrderOptions) (ccxt.Order, error) {
	m.calls = append(m.calls, "CreateOrder")
	return ccxt.Order{}, nil
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
