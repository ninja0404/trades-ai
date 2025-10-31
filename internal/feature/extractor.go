package feature

import (
	"context"
	"fmt"
	"math"
	"time"

	"go.uber.org/zap"

	"trades-ai/internal/exchange"
	"trades-ai/internal/indicator"
)

const (
	minCandles1H = 60
	minCandles4H = 30
)

// TrendFeatures 描述趋势相关指标。
type TrendFeatures struct {
	EMA12                float64
	EMA26                float64
	EMA50                float64
	EMARank              string
	PriceAboveEMA12      bool
	PriceAboveEMA26      bool
	PriceAboveEMA50      bool
	DistanceToEMA12      float64
	DistanceToEMA26      float64
	DistanceToEMA50      float64
	MACDValue            float64
	MACDSignal           float64
	MACDHistogram        float64
	MACDHistogramChange  float64
	BollingerPosition    float64
	BollingerBandwidth   float64
	HigherTimeframeTrend string
}

// MomentumFeatures 描述动量相关指标。
type MomentumFeatures struct {
	RSIValue         float64
	RSIState         string
	VolumeRatio      float64
	VolumeAverage20  float64
	VolumeDivergence string
}

// VolatilityFeatures 描述波动率状况。
type VolatilityFeatures struct {
	ATRAbsolute          float64
	ATRRelative          float64
	RecentVolatility     float64
	HistoricalVolatility float64
	VolatilityRatio      float64
}

// MarketStructureFeatures 描述市场结构。
type MarketStructureFeatures struct {
	SupportLevel       float64
	ResistanceLevel    float64
	PriceRange         float64
	OrderBookImbalance float64
	LargeOrderFlow     string
	BidAskSpread       float64
}

// MarketStateFeatures 描述整体市场状态。
type MarketStateFeatures struct {
	ADXValue       float64
	TrendStrength  string
	TradingSession string
}

// FeatureSet 汇总全部特征，用于后续提示词拼装。
type FeatureSet struct {
	Symbol          string
	GeneratedAt     time.Time
	Trend           TrendFeatures
	Momentum        MomentumFeatures
	Volatility      VolatilityFeatures
	MarketStructure MarketStructureFeatures
	MarketState     MarketStateFeatures
}

// Extractor 根据市场快照提取特征。
type Extractor struct {
	indicators *indicator.Calculator
	logger     *zap.Logger
}

// NewExtractor 创建特征提取器。
func NewExtractor(calc *indicator.Calculator, logger *zap.Logger) *Extractor {
	if calc == nil {
		calc = indicator.NewCalculator()
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Extractor{
		indicators: calc,
		logger:     logger,
	}
}

// Extract 计算特征。
func (e *Extractor) Extract(ctx context.Context, snapshot exchange.MarketSnapshot) (FeatureSet, error) {
	if len(snapshot.Candles1H) < minCandles1H {
		return FeatureSet{}, fmt.Errorf("1小时K线数量不足，至少需要 %d 根，当前 %d", minCandles1H, len(snapshot.Candles1H))
	}
	if len(snapshot.Candles4H) < minCandles4H {
		return FeatureSet{}, fmt.Errorf("4小时K线数量不足，至少需要 %d 根，当前 %d", minCandles4H, len(snapshot.Candles4H))
	}

	select {
	case <-ctx.Done():
		return FeatureSet{}, ctx.Err()
	default:
	}

	res1h, err := e.indicators.Compute(exchange.Timeframe1h, snapshot.Candles1H)
	if err != nil {
		return FeatureSet{}, fmt.Errorf("计算1小时指标失败: %w", err)
	}

	res4h, err := e.indicators.Compute(exchange.Timeframe4h, snapshot.Candles4H)
	if err != nil {
		return FeatureSet{}, fmt.Errorf("计算4小时指标失败: %w", err)
	}

	trend := e.buildTrendFeatures(res1h, res4h)
	momentum := e.buildMomentumFeatures(res1h)
	volatility := e.buildVolatilityFeatures(res1h)
	structure := e.buildMarketStructureFeatures(res1h, snapshot.OrderBook)
	state := e.buildMarketStateFeatures(res1h, snapshot.RetrievedAt)

	features := FeatureSet{
		Symbol:          snapshot.Symbol,
		GeneratedAt:     snapshot.RetrievedAt.UTC(),
		Trend:           trend,
		Momentum:        momentum,
		Volatility:      volatility,
		MarketStructure: structure,
		MarketState:     state,
	}

	e.logger.Debug("特征提取完成",
		zap.String("symbol", features.Symbol),
		zap.Time("generated_at", features.GeneratedAt),
	)

	return features, nil
}

func (e *Extractor) buildTrendFeatures(res1h, res4h indicator.Result) TrendFeatures {
	closePrice := clean(res1h.Close)

	distance12 := indicator.SafeDivide(closePrice-res1h.EMA12, closePrice)
	distance26 := indicator.SafeDivide(closePrice-res1h.EMA26, closePrice)
	distance50 := indicator.SafeDivide(closePrice-res1h.EMA50, closePrice)

	emaRank := determineEMARank(res1h.EMA12, res1h.EMA26, res1h.EMA50)
	htfTrend := determineHigherTimeframeTrend(res4h)

	macdChange := clean(res1h.MACD.Histogram - res1h.MACD.PrevHistogram)

	return TrendFeatures{
		EMA12:                clean(res1h.EMA12),
		EMA26:                clean(res1h.EMA26),
		EMA50:                clean(res1h.EMA50),
		EMARank:              emaRank,
		PriceAboveEMA12:      closePrice > res1h.EMA12,
		PriceAboveEMA26:      closePrice > res1h.EMA26,
		PriceAboveEMA50:      closePrice > res1h.EMA50,
		DistanceToEMA12:      clean(distance12),
		DistanceToEMA26:      clean(distance26),
		DistanceToEMA50:      clean(distance50),
		MACDValue:            clean(res1h.MACD.Value),
		MACDSignal:           clean(res1h.MACD.Signal),
		MACDHistogram:        clean(res1h.MACD.Histogram),
		MACDHistogramChange:  macdChange,
		BollingerPosition:    clean(res1h.Bollinger.Position),
		BollingerBandwidth:   clean(res1h.Bollinger.Bandwidth),
		HigherTimeframeTrend: htfTrend,
	}
}

func (e *Extractor) buildMomentumFeatures(res indicator.Result) MomentumFeatures {
	divergence := determineVolumeDivergence(res)

	return MomentumFeatures{
		RSIValue:         clean(res.RSI),
		RSIState:         determineRSIState(res.RSI),
		VolumeRatio:      clean(res.Volume.Ratio),
		VolumeAverage20:  clean(res.Volume.Average20),
		VolumeDivergence: divergence,
	}
}

func (e *Extractor) buildVolatilityFeatures(res indicator.Result) VolatilityFeatures {
	recentVol, historicalVol, ratio := computeVolatilityRatios(res.Series.Close)

	return VolatilityFeatures{
		ATRAbsolute:          clean(res.ATR.Absolute),
		ATRRelative:          clean(res.ATR.Relative),
		RecentVolatility:     clean(recentVol),
		HistoricalVolatility: clean(historicalVol),
		VolatilityRatio:      clean(ratio),
	}
}

func (e *Extractor) buildMarketStructureFeatures(res indicator.Result, orderBook exchange.OrderBookSnapshot) MarketStructureFeatures {
	support, resistance := computeSupportResistance(res.Series)
	priceRange := clean(resistance - support)
	imbalance := computeOrderBookImbalance(orderBook)
	flow := determineLargeOrderFlow(orderBook)
	spread := computeBidAskSpread(orderBook)

	return MarketStructureFeatures{
		SupportLevel:       clean(support),
		ResistanceLevel:    clean(resistance),
		PriceRange:         priceRange,
		OrderBookImbalance: clean(imbalance),
		LargeOrderFlow:     flow,
		BidAskSpread:       clean(spread),
	}
}

func (e *Extractor) buildMarketStateFeatures(res indicator.Result, ts time.Time) MarketStateFeatures {
	return MarketStateFeatures{
		ADXValue:       clean(res.ADX),
		TrendStrength:  determineTrendStrength(res.ADX),
		TradingSession: determineTradingSession(ts),
	}
}

func determineEMARank(ema12, ema26, ema50 float64) string {
	switch {
	case ema12 > ema26 && ema26 > ema50:
		return "bullish_alignment"
	case ema12 < ema26 && ema26 < ema50:
		return "bearish_alignment"
	default:
		return "mixed_alignment"
	}
}

func determineHigherTimeframeTrend(res indicator.Result) string {
	ema12 := clean(res.EMA12)
	ema26 := clean(res.EMA26)

	switch {
	case ema12 == 0 && ema26 == 0:
		return "unknown"
	case ema12 > ema26:
		return "bullish"
	case ema12 < ema26:
		return "bearish"
	default:
		return "neutral"
	}
}

func determineRSIState(rsi float64) string {
	rsi = clean(rsi)
	switch {
	case rsi >= 70:
		return "overbought"
	case rsi <= 30:
		return "oversold"
	default:
		return "neutral"
	}
}

func determineTrendStrength(adx float64) string {
	adx = clean(adx)
	switch {
	case adx < 20:
		return "range"
	case adx < 25:
		return "transition"
	case adx < 40:
		return "trending"
	default:
		return "strong_trend"
	}
}

func determineTradingSession(ts time.Time) string {
	hour := ts.UTC().Hour()
	switch {
	case hour >= 0 && hour < 8:
		return "asia"
	case hour >= 8 && hour < 16:
		return "europe"
	default:
		return "america"
	}
}

func determineVolumeDivergence(res indicator.Result) string {
	priceChange := clean(res.Close - res.PreviousClose)
	volumeRatio := clean(res.Volume.Ratio)

	switch {
	case priceChange > 0 && volumeRatio > 1:
		return "rally_with_volume"
	case priceChange > 0 && volumeRatio <= 1:
		return "rally_without_volume"
	case priceChange < 0 && volumeRatio > 1:
		return "selloff_with_volume"
	case priceChange < 0 && volumeRatio <= 1:
		return "selloff_without_volume"
	default:
		return "neutral"
	}
}

func computeVolatilityRatios(closes []float64) (recent, historical, ratio float64) {
	if len(closes) < 2 {
		return 0, 0, 0
	}

	returns := make([]float64, 0, len(closes)-1)
	for i := 1; i < len(closes); i++ {
		prev := closes[i-1]
		curr := closes[i]
		if prev == 0 {
			continue
		}
		returns = append(returns, (curr/prev)-1)
	}

	if len(returns) == 0 {
		return 0, 0, 0
	}

	recentWindow := min(14, len(returns))
	historicalWindow := min(60, len(returns))

	recent = stdDev(returns[len(returns)-recentWindow:])
	historical = stdDev(returns[len(returns)-historicalWindow:])
	ratio = indicator.SafeDivide(recent, historical)

	return recent, historical, ratio
}

func computeSupportResistance(series indicator.Series) (float64, float64) {
	window := min(50, series.Len())
	if window == 0 {
		return 0, 0
	}

	highs := series.High[series.Len()-window:]
	lows := series.Low[series.Len()-window:]

	resistance := highs[0]
	for _, v := range highs {
		if v > resistance {
			resistance = v
		}
	}

	support := lows[0]
	for _, v := range lows {
		if v < support {
			support = v
		}
	}

	return support, resistance
}

func computeOrderBookImbalance(orderBook exchange.OrderBookSnapshot) float64 {
	totalBid := 0.0
	totalAsk := 0.0

	depth := min(10, len(orderBook.Bids))
	for i := 0; i < depth; i++ {
		totalBid += orderBook.Bids[i].Amount
	}

	depth = min(10, len(orderBook.Asks))
	for i := 0; i < depth; i++ {
		totalAsk += orderBook.Asks[i].Amount
	}

	return indicator.SafeDivide(totalBid-totalAsk, totalBid+totalAsk)
}

func determineLargeOrderFlow(orderBook exchange.OrderBookSnapshot) string {
	depth := min(5, len(orderBook.Bids))
	bidVolume := 0.0
	for i := 0; i < depth; i++ {
		bidVolume += orderBook.Bids[i].Amount
	}

	depth = min(5, len(orderBook.Asks))
	askVolume := 0.0
	for i := 0; i < depth; i++ {
		askVolume += orderBook.Asks[i].Amount
	}

	if bidVolume == 0 && askVolume == 0 {
		return "neutral"
	}

	ratio := indicator.SafeDivide(bidVolume, askVolume)

	switch {
	case ratio > 1.2:
		return "buying_pressure"
	case ratio < 0.8:
		return "selling_pressure"
	default:
		return "balanced"
	}
}

func computeBidAskSpread(orderBook exchange.OrderBookSnapshot) float64 {
	if len(orderBook.Bids) == 0 || len(orderBook.Asks) == 0 {
		return 0
	}
	bestBid := orderBook.Bids[0].Price
	bestAsk := orderBook.Asks[0].Price
	return clean(bestAsk - bestBid)
}

func stdDev(values []float64) float64 {
	n := len(values)
	if n == 0 {
		return 0
	}
	mean := 0.0
	for _, v := range values {
		mean += v
	}
	mean /= float64(n)

	var variance float64
	for _, v := range values {
		diff := v - mean
		variance += diff * diff
	}
	variance /= float64(n)
	return math.Sqrt(variance)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clean(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}
