package monitor

import (
	"time"

	"trades-ai/internal/ai"
	"trades-ai/internal/execution"
	"trades-ai/internal/feature"
	"trades-ai/internal/position"
	"trades-ai/internal/risk"
)

// EventType 表示监控事件类型。
type EventType string

const (
	EventMarketSnapshot EventType = "market_snapshot"
	EventAIDecision     EventType = "ai_decision"
	EventRiskEvaluation EventType = "risk_evaluation"
	EventExecution      EventType = "execution"
	EventPosition       EventType = "position"
	EventError          EventType = "error"
)

// Event 封装通用监控事件。
type Event struct {
	Type      EventType   `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	Payload   interface{} `json:"payload"`
}

// MarketSnapshotPayload 记录行情与特征快照。
type MarketSnapshotPayload struct {
	Features feature.FeatureSet `json:"features"`
}

// AIDecisionPayload 记录AI决策。
type AIDecisionPayload struct {
	Decision ai.Decision        `json:"decision"`
	Features feature.FeatureSet `json:"features"`
}

// RiskEvaluationPayload 记录风控评估过程。
type RiskEvaluationPayload struct {
	Input  risk.EvaluationInput  `json:"input"`
	Result risk.EvaluationResult `json:"result"`
}

// ExecutionPayload 记录订单执行结果。
type ExecutionPayload struct {
	Plan   execution.ExecutionPlan `json:"plan"`
	Result execution.Result        `json:"result"`
}

// PositionPayload 追踪账户与持仓。
type PositionPayload struct {
	Balance   position.AccountBalance   `json:"balance"`
	Positions []position.PositionDetail `json:"positions"`
}

// ErrorPayload 记录异常。
type ErrorPayload struct {
	Message string                 `json:"message"`
	Error   string                 `json:"error"`
	Context map[string]interface{} `json:"context,omitempty"`
}
