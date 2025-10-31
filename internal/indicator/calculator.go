package indicator

import (
	"fmt"
	"math"
	"sync"

	talib "github.com/markcheno/go-talib"

	"trades-ai/internal/exchange"
)

// MACDResult 保存 MACD 关键值。
type MACDResult struct {
	Value         float64
	Signal        float64
	Histogram     float64
	PrevHistogram float64
}

// BollingerResult 保存布林带数据。
type BollingerResult struct {
	Upper     float64
	Middle    float64
	Lower     float64
	Bandwidth float64
	Position  float64
}

// ATRResult 保存 ATR 指标。
type ATRResult struct {
	Absolute     float64
	Relative     float64
	PrevAbsolute float64
}

// VolumeResult 保存成交量相关统计。
type VolumeResult struct {
	Current   float64
	Average20 float64
	Ratio     float64
}

// Result 为一次指标计算的汇总。
type Result struct {
	Timeframe     string
	Series        Series
	EMA12         float64
	EMA26         float64
	EMA50         float64
	MACD          MACDResult
	Bollinger     BollingerResult
	RSI           float64
	ATR           ATRResult
	ADX           float64
	Volume        VolumeResult
	Close         float64
	PreviousClose float64
}

type cacheEntry struct {
	key    string
	result Result
}

// Calculator 提供技术指标计算并带有简单缓存。
type Calculator struct {
	mu    sync.Mutex
	cache map[string]cacheEntry
}

// NewCalculator 创建 Calculator。
func NewCalculator() *Calculator {
	return &Calculator{
		cache: make(map[string]cacheEntry),
	}
}

// Compute 依据给定K线计算常用技术指标。
func (c *Calculator) Compute(timeframe string, candles []exchange.Candle) (Result, error) {
	if len(candles) == 0 {
		return Result{}, fmt.Errorf("计算指标失败: 输入K线为空")
	}

	series := NewSeries(candles)
	cacheKey := fmt.Sprintf("%s:%d:%d", timeframe, series.Len(), series.Timestamps[len(series.Timestamps)-1].Unix())

	c.mu.Lock()
	if entry, ok := c.cache[timeframe]; ok && entry.key == cacheKey {
		c.mu.Unlock()
		return entry.result, nil
	}
	c.mu.Unlock()

	result, err := c.calculate(timeframe, series)
	if err != nil {
		return Result{}, err
	}

	c.mu.Lock()
	c.cache[timeframe] = cacheEntry{key: cacheKey, result: result}
	c.mu.Unlock()

	return result, nil
}

func (c *Calculator) calculate(timeframe string, series Series) (Result, error) {
	closePrices := series.Close
	highs := series.High
	lows := series.Low
	volumes := series.Volume

	ema12 := talib.Ema(closePrices, 12)
	ema26 := talib.Ema(closePrices, 26)
	ema50 := talib.Ema(closePrices, 50)

	macd, macdSignal, macdHist := talib.Macd(closePrices, 12, 26, 9)

	bbUpper, bbMiddle, bbLower := talib.BBands(closePrices, 20, 2, 2, talib.EMA)

	rsi := talib.Rsi(closePrices, 14)

	atr := talib.Atr(highs, lows, closePrices, 14)

	adx := talib.Adx(highs, lows, closePrices, 14)

	volumeAvg20 := average(SliceTail(volumes, 20))
	volumeCurrent := Last(volumes)
	volumeRatio := SafeDivide(volumeCurrent, volumeAvg20)

	lastClose := Last(closePrices)
	prevClose := Prev(closePrices)

	atrAbs := Last(atr)
	prevAtr := Prev(atr)
	atrRel := SafeDivide(atrAbs, lastClose)

	bollinger := buildBollinger(closePrices, bbUpper, bbMiddle, bbLower)

	result := Result{
		Timeframe:     timeframe,
		Series:        series,
		EMA12:         Last(ema12),
		EMA26:         Last(ema26),
		EMA50:         Last(ema50),
		MACD:          buildMACD(macd, macdSignal, macdHist),
		Bollinger:     bollinger,
		RSI:           Last(rsi),
		ATR:           ATRResult{Absolute: atrAbs, Relative: atrRel, PrevAbsolute: prevAtr},
		ADX:           Last(adx),
		Volume:        VolumeResult{Current: volumeCurrent, Average20: volumeAvg20, Ratio: volumeRatio},
		Close:         lastClose,
		PreviousClose: prevClose,
	}

	return result, nil
}

func buildMACD(macd, signal, hist []float64) MACDResult {
	return MACDResult{
		Value:         Last(macd),
		Signal:        Last(signal),
		Histogram:     Last(hist),
		PrevHistogram: Prev(hist),
	}
}

func buildBollinger(close, upper, middle, lower []float64) BollingerResult {
	u := Last(upper)
	m := Last(middle)
	l := Last(lower)
	histWidth := u - l
	bandwidth := SafeDivide(histWidth, m)

	position := 0.0
	if histWidth > 0 {
		position = SafeDivide(Last(close)-l, histWidth)
	}

	// 将位置限制在[0,1]区间，便于后续使用。
	position = math.Max(0, math.Min(1, position))

	return BollingerResult{
		Upper:     u,
		Middle:    m,
		Lower:     l,
		Bandwidth: bandwidth,
		Position:  position,
	}
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}
