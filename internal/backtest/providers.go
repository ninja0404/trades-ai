package backtest

import (
	"context"
	"errors"

	"trades-ai/internal/ai"
	"trades-ai/internal/exchange"
	"trades-ai/internal/feature"
	"trades-ai/internal/position"
)

// SliceSnapshotProvider 以固定序列提供快照。
type SliceSnapshotProvider struct {
	snapshots []exchange.MarketSnapshot
	index     int
}

func NewSliceSnapshotProvider(snaps []exchange.MarketSnapshot) *SliceSnapshotProvider {
	return &SliceSnapshotProvider{snapshots: snaps}
}

func (p *SliceSnapshotProvider) Next(ctx context.Context) (exchange.MarketSnapshot, bool, error) {
	if p.index >= len(p.snapshots) {
		return exchange.MarketSnapshot{}, false, nil
	}
	snap := p.snapshots[p.index]
	p.index++
	return snap, true, nil
}

// DecisionProviderFunc 允许使用函数作为决策提供者。
type DecisionProviderFunc func(ctx context.Context, features feature.FeatureSet, pos position.Summary) (ai.Decision, error)

func (f DecisionProviderFunc) Decide(ctx context.Context, features feature.FeatureSet, pos position.Summary) (ai.Decision, error) {
	if f == nil {
		return ai.Decision{}, errors.New("backtest: 决策函数未实现")
	}
	return f(ctx, features, pos)
}
