package backtest

import (
	"context"

	"trades-ai/internal/ai"
	"trades-ai/internal/exchange"
	"trades-ai/internal/feature"
	"trades-ai/internal/position"
)

// SnapshotProvider 按时间顺序提供市场快照。
type SnapshotProvider interface {
	Next(ctx context.Context) (exchange.MarketSnapshot, bool, error)
}

// DecisionProvider 提供AI决策接口，便于在回测中注入不同源。
type DecisionProvider interface {
	Decide(ctx context.Context, features feature.FeatureSet, pos position.Summary) (ai.Decision, error)
}
