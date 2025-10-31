package backtest

import "math"

// Metrics 记录回测绩效指标。
type Metrics struct {
	TotalReturn float64
	MaxDrawdown float64
	SharpeRatio float64
}

func calculateMetrics(equity []float64, returns []float64) Metrics {
	if len(equity) == 0 {
		return Metrics{}
	}

	initial := equity[0]
	final := equity[len(equity)-1]
	totalReturn := 0.0
	if initial > 0 {
		totalReturn = final/initial - 1
	}

	maxDrawdown := computeDrawdown(equity)
	sharpe := computeSharpe(returns)

	return Metrics{
		TotalReturn: totalReturn,
		MaxDrawdown: maxDrawdown,
		SharpeRatio: sharpe,
	}
}

func computeDrawdown(equity []float64) float64 {
	var peak float64
	maxDD := 0.0
	for _, v := range equity {
		if v > peak {
			peak = v
		}
		if peak <= 0 {
			continue
		}
		dd := (v - peak) / peak
		if dd < maxDD {
			maxDD = dd
		}
	}
	return math.Abs(maxDD)
}

func computeSharpe(returns []float64) float64 {
	if len(returns) == 0 {
		return 0
	}
	mean := 0.0
	for _, r := range returns {
		mean += r
	}
	mean /= float64(len(returns))

	variance := 0.0
	for _, r := range returns {
		diff := r - mean
		variance += diff * diff
	}
	if len(returns) > 1 {
		variance /= float64(len(returns) - 1)
	}

	std := math.Sqrt(variance)
	if std == 0 {
		return 0
	}

	// 假设每步为1小时，换算年化：sqrt(24*365)
	annualFactor := math.Sqrt(24 * 365)
	return (mean / std) * annualFactor
}
