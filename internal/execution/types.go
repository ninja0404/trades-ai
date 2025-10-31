package execution

import (
	"time"

	"trades-ai/internal/ai"
	"trades-ai/internal/position"
	"trades-ai/internal/risk"
)

// OrderSide 表示下单方向。
type OrderSide string

const (
	OrderSideBuy  OrderSide = "buy"
	OrderSideSell OrderSide = "sell"
)

// ExecutionPlan 描述一次交易执行的目标。
type ExecutionPlan struct {
	Symbol          string
	Side            OrderSide
	CurrentExposure float64
	TargetExposure  float64
	MarketPrice     float64
	RiskAmount      float64
	Decision        ai.Decision
	RiskResult      risk.EvaluationResult
	Account         position.AccountBalance
	Position        position.Summary
	GeneratedAt     time.Time
}

// OrderRequest 抽象具体委托。
type OrderRequest struct {
	Type         string // market | limit
	Side         OrderSide
	Amount       float64
	Price        float64
	ReduceOnly   bool
	CloseAll     bool
	ClientOrder  string
	Params       map[string]interface{}
	IsTrigger    bool
	TriggerType  string
	TriggerPrice float64
}

// Result 为执行结果摘要。
type Result struct {
	Orders        []OrderRequest
	Executed      bool
	ExecutionTime time.Time
	Notes         []string
}
