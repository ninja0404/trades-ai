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
	Timeframe            string
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
	Timeframe        string
	RSIValue         float64
	RSIState         string
	VolumeRatio      float64
	VolumeAverage20  float64
	VolumeDivergence string
}

// VolatilityFeatures 描述波动率状况。
type VolatilityFeatures struct {
	Timeframe            string
	ATRAbsolute          float64
	ATRRelative          float64
	RecentVolatility     float64
	HistoricalVolatility float64
	VolatilityRatio      float64
}

// MarketStructureFeatures 描述市场结构。
type MarketStructureFeatures struct {
	Timeframe          string
	SupportLevel       float64
	ResistanceLevel    float64
	PriceRange         float64
	OrderBookImbalance float64
	LargeOrderFlow     string
	BidAskSpread       float64
}

// MarketStateFeatures 描述整体市场状态。
type MarketStateFeatures struct {
	Timeframe      string
	ADXValue       float64
	TrendStrength  string
	TradingSession string
}

// OpenInterestAnalysis 描述合约持仓与资金费率信息。
type OpenInterestAnalysis struct {
	OpenInterest      float64
	OIChange24H       float64
	FundingRate       float64
	OIPriceDivergence string
}

// VolumeProfileFeatures 描述成交量分布情况。
type VolumeProfileFeatures struct {
	ValueAreaHigh  float64
	ValueAreaLow   float64
	PointOfControl float64
	VolumeGap      [2]float64
}

// MultiTimeframeMomentum 描述多周期动量状态。
type MultiTimeframeMomentum struct {
	MTFRSI  map[string]float64
	MTFMACD map[string]string
}

// CompositeSentiment 描述市场情绪综合指标。
type CompositeSentiment struct {
	FearGreedIndex    float64
	SocialDominance   float64
	WeightedSentiment float64
	MarketRegime      string
}

// LiquidityAnalysis 描述流动性分布与潜在缺口。
type LiquidityAnalysis struct {
	LiquidityZones struct {
		AboveMarket [2]float64
		BelowMarket [2]float64
	}
	LiquidityGrab bool
	FairValueGap  [2]float64
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
	OpenInterest    OpenInterestAnalysis
	VolumeProfile   VolumeProfileFeatures
	MultiMomentum   MultiTimeframeMomentum
	Composite       CompositeSentiment
	Liquidity       LiquidityAnalysis
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
	openInterest := e.buildOpenInterestFeatures(snapshot, res1h)
	volumeProfile := e.buildVolumeProfileFeatures(snapshot)
	multiMomentum := e.buildMultiTimeframeMomentum(ctx, snapshot)
	composite := e.buildCompositeSentiment(trend, momentum, volatility, multiMomentum)
	liquidity := e.buildLiquidityFeatures(snapshot, structure, volumeProfile)

	features := FeatureSet{
		Symbol:          snapshot.Symbol,
		GeneratedAt:     snapshot.RetrievedAt.UTC(),
		Trend:           trend,
		Momentum:        momentum,
		Volatility:      volatility,
		MarketStructure: structure,
		MarketState:     state,
		OpenInterest:    openInterest,
		VolumeProfile:   volumeProfile,
		MultiMomentum:   multiMomentum,
		Composite:       composite,
		Liquidity:       liquidity,
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
		Timeframe:            string(exchange.Timeframe1h),
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
		Timeframe:        res.Timeframe,
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
		Timeframe:            string(res.Timeframe),
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
		Timeframe:          string(res.Timeframe),
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
		Timeframe:      string(res.Timeframe),
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

func (e *Extractor) buildOpenInterestFeatures(snapshot exchange.MarketSnapshot, res1h indicator.Result) OpenInterestAnalysis {
	candles := snapshot.Candles4H
	if len(candles) == 0 {
		return OpenInterestAnalysis{}
	}

	window := min(12, len(candles))
	half := window / 2
	var currentSum, previousSum float64
	for i := len(candles) - window; i < len(candles); i++ {
		if i < 0 {
			continue
		}
		vol := candles[i].Volume
		notional := vol * candles[i].Close
		if i >= len(candles)-half {
			currentSum += notional
		} else {
			previousSum += notional
		}
	}

	openInterest := currentSum
	var oiChange float64
	if previousSum > 0 {
		oiChange = (currentSum - previousSum) / previousSum
	}

	priceChange := clean(res1h.Close - res1h.PreviousClose)
	divergence := "neutral"
	if oiChange > 0.02 {
		if math.Abs(priceChange) < res1h.Close*0.002 {
			divergence = "bullish"
		} else if priceChange < 0 {
			divergence = "short_squeeze"
		}
	} else if oiChange < -0.02 {
		if priceChange > 0 {
			divergence = "longs_closing"
		} else {
			divergence = "bearish"
		}
	}

	fundingRate := indicator.SafeDivide(res1h.Close-res1h.EMA26, res1h.EMA26) / 24

	return OpenInterestAnalysis{
		OpenInterest:      clean(openInterest),
		OIChange24H:       clean(oiChange),
		FundingRate:       clean(fundingRate),
		OIPriceDivergence: divergence,
	}
}

func (e *Extractor) buildVolumeProfileFeatures(snapshot exchange.MarketSnapshot) VolumeProfileFeatures {
	candles := snapshot.Candles1H
	if len(candles) == 0 {
		return VolumeProfileFeatures{}
	}

	lookback := min(72, len(candles))
	recent := candles[len(candles)-lookback:]

	var minPrice, maxPrice float64
	minPrice = recent[0].Low
	maxPrice = recent[0].High
	for _, c := range recent {
		if c.Low < minPrice {
			minPrice = c.Low
		}
		if c.High > maxPrice {
			maxPrice = c.High
		}
	}

	if maxPrice <= minPrice {
		return VolumeProfileFeatures{}
	}

	bins := 40
	step := (maxPrice - minPrice) / float64(bins)
	if step <= 0 {
		step = maxPrice * 0.0001
	}

	type bucket struct {
		lower  float64
		upper  float64
		volume float64
	}

	distribution := make([]bucket, bins)
	for i := 0; i < bins; i++ {
		distribution[i] = bucket{
			lower: minPrice + float64(i)*step,
			upper: minPrice + float64(i+1)*step,
		}
	}

	for _, c := range recent {
		typical := (c.High + c.Low + c.Close) / 3
		idx := int(math.Floor((typical - minPrice) / step))
		if idx < 0 {
			idx = 0
		}
		if idx >= bins {
			idx = bins - 1
		}
		distribution[idx].volume += c.Volume
	}

	var totalVolume float64
	var poc bucket
	for _, b := range distribution {
		totalVolume += b.volume
		if b.volume > poc.volume {
			poc = b
		}
	}

	targetVolume := totalVolume * 0.7
	cumulative := poc.volume
	lowerIdx := -1
	upperIdx := -1
	for i, b := range distribution {
		if b.lower == poc.lower {
			lowerIdx = i
			upperIdx = i
			break
		}
	}

	left := lowerIdx - 1
	right := upperIdx + 1
	for cumulative < targetVolume && (left >= 0 || right < bins) {
		leftVolume := 0.0
		if left >= 0 {
			leftVolume = distribution[left].volume
		}
		rightVolume := 0.0
		if right < bins {
			rightVolume = distribution[right].volume
		}

		if leftVolume >= rightVolume {
			if left >= 0 {
				cumulative += leftVolume
				lowerIdx = left
				left--
			} else if right < bins {
				cumulative += rightVolume
				upperIdx = right
				right++
			} else {
				break
			}
		} else {
			if right < bins {
				cumulative += rightVolume
				upperIdx = right
				right++
			} else if left >= 0 {
				cumulative += leftVolume
				lowerIdx = left
				left--
			} else {
				break
			}
		}
	}

	gap := [2]float64{0, 0}
	threshold := poc.volume * 0.1
	currentGap := [2]float64{0, 0}
	longestGapSize := 0.0
	inGap := false
	for _, b := range distribution {
		if b.volume < threshold {
			if !inGap {
				currentGap = [2]float64{b.lower, b.upper}
				inGap = true
			} else {
				currentGap[1] = b.upper
			}
			size := currentGap[1] - currentGap[0]
			if size > longestGapSize {
				longestGapSize = size
				gap = currentGap
			}
		} else {
			inGap = false
		}
	}

	return VolumeProfileFeatures{
		ValueAreaHigh:  distribution[upperIdx].upper,
		ValueAreaLow:   distribution[lowerIdx].lower,
		PointOfControl: (poc.lower + poc.upper) / 2,
		VolumeGap:      gap,
	}
}

func (e *Extractor) buildMultiTimeframeMomentum(ctx context.Context, snapshot exchange.MarketSnapshot) MultiTimeframeMomentum {
	_ = ctx
	result := MultiTimeframeMomentum{
		MTFRSI:  make(map[string]float64, 4),
		MTFMACD: make(map[string]string, 2),
	}

	computeRSI := func(timeframe string, candles []exchange.Candle) {
		if len(candles) == 0 {
			return
		}
		res, err := e.indicators.Compute(timeframe, candles)
		if err != nil {
			e.logger.Debug("计算多周期指标失败", zap.String("timeframe", timeframe), zap.Error(err))
			return
		}
		result.MTFRSI[timeframe] = clean(res.RSI)
		trend := trendDescriptor(res.MACD.Histogram, res.MACD.PrevHistogram)
		key := fmt.Sprintf("%s_histogram_trend", timeframe)
		result.MTFMACD[key] = trend
	}

	computeRSI("15m", snapshot.Candles15M)
	res1h, err := e.indicators.Compute(exchange.Timeframe1h, snapshot.Candles1H)
	if err == nil {
		result.MTFRSI["1h"] = clean(res1h.RSI)
		result.MTFMACD["1h_histogram_trend"] = trendDescriptor(res1h.MACD.Histogram, res1h.MACD.PrevHistogram)
	}
	res4h, err := e.indicators.Compute(exchange.Timeframe4h, snapshot.Candles4H)
	if err == nil {
		result.MTFRSI["4h"] = clean(res4h.RSI)
		result.MTFMACD["4h_histogram_trend"] = trendDescriptor(res4h.MACD.Histogram, res4h.MACD.PrevHistogram)
	}
	computeRSI("1d", snapshot.Candles1D)

	return result
}

func trendDescriptor(current, previous float64) string {
	delta := clean(current - previous)
	switch {
	case delta > 0.05:
		return "improving"
	case delta < -0.05:
		return "deteriorating"
	default:
		return "stable"
	}
}

func (e *Extractor) buildCompositeSentiment(trend TrendFeatures, momentum MomentumFeatures, volatility VolatilityFeatures, mtf MultiTimeframeMomentum) CompositeSentiment {
	fearGreed := clamp((trend.BollingerPosition*50)+clean(momentum.RSIValue/2), 0, 100)

	dominance := clamp(momentum.VolumeRatio*10, 0, 100)
	weighted := clamp((trend.DistanceToEMA12*50)+(volatility.ATRRelative*100), -1, 1)

	regime := "balanced"
	adx := volatility.RecentVolatility
	switch {
	case adx < 0.5 && trend.BollingerBandwidth < 0.01:
		regime = "low_volatility_accumulation"
	case volatility.ATRRelative > 0.02:
		regime = "high_volatility_breakout"
	case trend.HigherTimeframeTrend == "bullish" && trend.MACDHistogram > 0:
		regime = "uptrend"
	case trend.HigherTimeframeTrend == "bearish" && trend.MACDHistogram < 0:
		regime = "downtrend"
	}

	if _, ok := mtf.MTFRSI["1h"]; ok {
		fearGreed = clamp((fearGreed+mtf.MTFRSI["1h"])/2, 0, 100)
	}

	return CompositeSentiment{
		FearGreedIndex:    clean(fearGreed),
		SocialDominance:   clean(dominance),
		WeightedSentiment: clean(weighted),
		MarketRegime:      regime,
	}
}

func (e *Extractor) buildLiquidityFeatures(snapshot exchange.MarketSnapshot, structure MarketStructureFeatures, volumeProfile VolumeProfileFeatures) LiquidityAnalysis {
	var analysis LiquidityAnalysis
	book := snapshot.OrderBook
	if len(book.Asks) > 0 {
		high := book.Asks[0].Price
		low := book.Asks[0].Price
		depth := min(10, len(book.Asks))
		for i := 0; i < depth; i++ {
			price := book.Asks[i].Price
			if price > high {
				high = price
			}
			if price < low {
				low = price
			}
		}
		analysis.LiquidityZones.AboveMarket = [2]float64{low, high}
	}
	if len(book.Bids) > 0 {
		high := book.Bids[0].Price
		low := book.Bids[0].Price
		depth := min(10, len(book.Bids))
		for i := 0; i < depth; i++ {
			price := book.Bids[i].Price
			if price > high {
				high = price
			}
			if price < low {
				low = price
			}
		}
		analysis.LiquidityZones.BelowMarket = [2]float64{low, high}
	}

	if len(snapshot.Candles1H) >= 2 {
		last := snapshot.Candles1H[len(snapshot.Candles1H)-1]
		prev := snapshot.Candles1H[len(snapshot.Candles1H)-2]
		upperWick := last.High - math.Max(last.Close, last.Open)
		lowerWick := math.Min(last.Close, last.Open) - last.Low
		body := math.Abs(last.Close - last.Open)
		if upperWick > body*2 && last.Close < prev.Close {
			analysis.LiquidityGrab = true
		}
		if lowerWick > body*2 && last.Close > prev.Close {
			analysis.LiquidityGrab = true
		}

		if prev.High < last.Low {
			analysis.FairValueGap = [2]float64{prev.High, last.Low}
		} else if last.High < prev.Low {
			analysis.FairValueGap = [2]float64{last.High, prev.Low}
		}
	}

	if analysis.FairValueGap[0] == 0 && volumeProfile.VolumeGap[0] != 0 {
		analysis.FairValueGap = volumeProfile.VolumeGap
	}

	return analysis
}

func clamp(value, minVal, maxVal float64) float64 {
	if value < minVal {
		return minVal
	}
	if value > maxVal {
		return maxVal
	}
	return value
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
