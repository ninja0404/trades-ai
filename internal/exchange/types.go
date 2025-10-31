package exchange

import "time"

const (
	// Timeframe1h 为主决策周期。
	Timeframe1h = "1h"
	// Timeframe4h 为趋势过滤周期。
	Timeframe4h = "4h"
)

// Candle 代表单根K线。
type Candle struct {
	Timestamp time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
}

// OrderBookLevel 表示盘口档位。
type OrderBookLevel struct {
	Price  float64
	Amount float64
}

// OrderBookSnapshot 为订单簿快照。
type OrderBookSnapshot struct {
	Symbol    string
	Bids      []OrderBookLevel
	Asks      []OrderBookLevel
	Timestamp time.Time
	Nonce     int64
}

// MarketSnapshot 聚合多个时间框架及盘口数据。
type MarketSnapshot struct {
	Symbol      string
	Candles1H   []Candle
	Candles4H   []Candle
	OrderBook   OrderBookSnapshot
	RetrievedAt time.Time
}

// SnapshotRequest 控制一次快照采集的参数。
type SnapshotRequest struct {
	Limit1H        int
	Limit4H        int
	OrderBookDepth int
}

// DefaultSnapshotRequest 返回默认快照参数。
func DefaultSnapshotRequest() SnapshotRequest {
	return SnapshotRequest{
		Limit1H:        200,
		Limit4H:        200,
		OrderBookDepth: 100,
	}
}
