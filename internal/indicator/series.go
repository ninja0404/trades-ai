package indicator

import (
	"math"
	"time"

	"trades-ai/internal/exchange"
)

// Series 将K线数据拆分为便于指标计算的序列。
type Series struct {
	Timestamps []time.Time
	Open       []float64
	High       []float64
	Low        []float64
	Close      []float64
	Volume     []float64
}

// NewSeries 从交易所K线创建 Series，按时间升序排列。
func NewSeries(candles []exchange.Candle) Series {
	length := len(candles)
	series := Series{
		Timestamps: make([]time.Time, length),
		Open:       make([]float64, length),
		High:       make([]float64, length),
		Low:        make([]float64, length),
		Close:      make([]float64, length),
		Volume:     make([]float64, length),
	}

	for i := 0; i < length; i++ {
		candle := candles[i]
		series.Timestamps[i] = candle.Timestamp.UTC()
		series.Open[i] = candle.Open
		series.High[i] = candle.High
		series.Low[i] = candle.Low
		series.Close[i] = candle.Close
		series.Volume[i] = candle.Volume
	}

	return series
}

// Len 返回序列长度。
func (s Series) Len() int {
	return len(s.Close)
}

// Last 返回序列最后一个值，若为空则返回 NaN。
func Last(values []float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	return values[len(values)-1]
}

// Prev 返回序列倒数第二个值，若不足两个元素则返回 NaN。
func Prev(values []float64) float64 {
	if len(values) < 2 {
		return math.NaN()
	}
	return values[len(values)-2]
}

// SliceTail 返回序列末尾 n 个值，不足时返回全部。
func SliceTail(values []float64, n int) []float64 {
	if n <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) <= n {
		dst := make([]float64, len(values))
		copy(dst, values)
		return dst
	}
	dst := make([]float64, n)
	copy(dst, values[len(values)-n:])
	return dst
}

// SafeDivide 除法保护，除数为0时返回0。
func SafeDivide(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}
