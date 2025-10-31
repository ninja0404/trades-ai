package execution

import "context"

// Trader 抽象执行器接口，方便切换真实或模拟下单。
type Trader interface {
	BuildPlan(plan ExecutionPlan) ([]OrderRequest, error)
	Execute(ctx context.Context, orders []OrderRequest) (Result, error)
}

var _ Trader = (*Executor)(nil)
