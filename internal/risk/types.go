package risk

import (
	"time"

	"trades-ai/internal/ai"
	"trades-ai/internal/feature"
	"trades-ai/internal/position"
)

// StatusType 描述风险评估结果状态。
type StatusType string

const (
	StatusProceed StatusType = "proceed"
	StatusDeny    StatusType = "deny"
	StatusExit    StatusType = "exit_only"
)

// AccountState 表示账户资金状况与当前风险暴露。
type AccountState struct {
	Equity                 float64   // 当前账户净值
	Balance                float64   // 账户余额
	CurrentExposurePercent float64   // 已占用的总仓位（占净值比例，0-1）
	Timestamp              time.Time // 评估时间
}

// EvaluationInput 为风险评估输入。
type EvaluationInput struct {
	Symbol      string
	Decision    ai.Decision
	Features    feature.FeatureSet
	Position    position.Summary
	Account     AccountState
	MarketPrice float64
}

// DailyStatus 表示当日风控状态。
type DailyStatus struct {
	TradingDate   string
	StartEquity   float64
	CurrentEquity float64
	LossPercent   float64
	Halted        bool
}

// EvaluationResult 为风险评估输出。
type EvaluationResult struct {
	Symbol                string
	Status                StatusType
	TargetExposurePercent float64
	RecommendedStopLoss   float64
	RecommendedTakeProfit float64
	RiskAmount            float64
	ConfidenceApplied     float64
	Notes                 []string
	DailyStatus           DailyStatus
}
