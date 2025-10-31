package backtest

import (
	"math"
	"time"

	"trades-ai/internal/position"
)

// Simulator 根据目标仓位模拟账户权益变化。
type Simulator struct {
	initialEquity float64
	equity        float64
	exposure      float64
	lastPrice     float64

	entryPrice    float64
	positionStart time.Time

	equityHistory   []float64
	returnHistory   []float64
	exposureHistory []float64
	tradeCount      int
}

func NewSimulator(initialEquity float64) *Simulator {
	if initialEquity <= 0 {
		initialEquity = 10000
	}
	return &Simulator{
		initialEquity: initialEquity,
		equity:        initialEquity,
		equityHistory: []float64{initialEquity},
	}
}

// Advance 根据最新价格计算未平仓仓位的损益。
func (s *Simulator) Advance(price float64) {
	if price <= 0 {
		return
	}
	if s.lastPrice > 0 && s.exposure != 0 {
		ret := price/s.lastPrice - 1
		prevEquity := s.equity
		pnl := prevEquity * s.exposure * ret
		s.equity = prevEquity + pnl
		if prevEquity != 0 {
			s.returnHistory = append(s.returnHistory, pnl/prevEquity)
		}
	}
	s.lastPrice = price
	s.equityHistory = append(s.equityHistory, s.equity)
	s.exposureHistory = append(s.exposureHistory, s.exposure)
}

// AdjustExposure 在给定价格下调整仓位。
func (s *Simulator) AdjustExposure(target float64, price float64, ts time.Time) {
	if math.Abs(target-s.exposure) < 1e-6 {
		return
	}
	s.tradeCount++
	if math.Abs(target) < 1e-6 {
		s.exposure = 0
		s.entryPrice = 0
		s.positionStart = time.Time{}
		return
	}
	if s.exposure == 0 || (target > 0 && s.exposure <= 0) || (target < 0 && s.exposure >= 0) {
		s.entryPrice = price
		s.positionStart = ts
	}
	s.exposure = target
}

func (s *Simulator) Equity() float64 {
	return s.equity
}

func (s *Simulator) Exposure() float64 {
	return s.exposure
}

func (s *Simulator) TradeCount() int {
	return s.tradeCount
}

func (s *Simulator) EquityHistory() []float64 {
	return append([]float64(nil), s.equityHistory...)
}

func (s *Simulator) ReturnHistory() []float64 {
	return append([]float64(nil), s.returnHistory...)
}

func (s *Simulator) Summary(price float64, ts time.Time) position.Summary {
	side := ""
	if s.exposure > 0 {
		side = "LONG"
	} else if s.exposure < 0 {
		side = "SHORT"
	}

	sizePercent := math.Abs(s.exposure) * 100
	pnlPercent := 0.0
	if s.entryPrice > 0 && price > 0 {
		switch side {
		case "LONG":
			pnlPercent = (price - s.entryPrice) / s.entryPrice * 100
		case "SHORT":
			pnlPercent = (s.entryPrice - price) / s.entryPrice * 100
		}
	}

	var age float64
	if !s.positionStart.IsZero() && !ts.IsZero() {
		age = ts.Sub(s.positionStart).Hours()
	}

	return position.Summary{
		Side:                 side,
		SizePercent:          sizePercent,
		EntryPrice:           s.entryPrice,
		UnrealizedPnlPercent: pnlPercent,
		PositionAgeHours:     age,
		StopLoss:             0,
		TakeProfit:           0,
	}
}
