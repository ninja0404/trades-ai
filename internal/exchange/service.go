package exchange

import (
	"context"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// MarketDataService 聚合K线及盘口数据获取。
type MarketDataService struct {
	client *Client
	logger *zap.Logger
}

// NewMarketDataService 创建市场数据服务。
func NewMarketDataService(client *Client, logger *zap.Logger) *MarketDataService {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &MarketDataService{
		client: client,
		logger: logger,
	}
}

// GetSnapshot 拉取包含1小时、4小时K线及订单簿的市场数据快照。
func (s *MarketDataService) GetSnapshot(ctx context.Context, req SnapshotRequest) (MarketSnapshot, error) {
	defaultReq := DefaultSnapshotRequest()
	if req.Limit1H <= 0 {
		req.Limit1H = defaultReq.Limit1H
	}
	if req.Limit4H <= 0 {
		req.Limit4H = defaultReq.Limit4H
	}
	if req.Limit15M <= 0 {
		req.Limit15M = defaultReq.Limit15M
	}
	if req.Limit1D <= 0 {
		req.Limit1D = defaultReq.Limit1D
	}
	if req.OrderBookDepth <= 0 {
		req.OrderBookDepth = defaultReq.OrderBookDepth
	}

	var (
		candles1H  []Candle
		candles4H  []Candle
		candles15M []Candle
		candles1D  []Candle
		orderBook  OrderBookSnapshot
	)

	group, groupCtx := errgroup.WithContext(ctx)

	group.Go(func() error {
		data, err := s.client.FetchCandles(groupCtx, Timeframe1h, int64(req.Limit1H))
		if err != nil {
			return err
		}
		candles1H = data
		return nil
	})

	group.Go(func() error {
		data, err := s.client.FetchCandles(groupCtx, Timeframe4h, int64(req.Limit4H))
		if err != nil {
			return err
		}
		candles4H = data
		return nil
	})

	group.Go(func() error {
		data, err := s.client.FetchCandles(groupCtx, "15m", int64(req.Limit15M))
		if err != nil {
			return err
		}
		candles15M = data
		return nil
	})

	group.Go(func() error {
		data, err := s.client.FetchCandles(groupCtx, "1d", int64(req.Limit1D))
		if err != nil {
			return err
		}
		candles1D = data
		return nil
	})

	group.Go(func() error {
		book, err := s.client.FetchOrderBook(groupCtx, int64(req.OrderBookDepth))
		if err != nil {
			return err
		}
		orderBook = book
		return nil
	})

	if err := group.Wait(); err != nil {
		return MarketSnapshot{}, err
	}

	snapshot := MarketSnapshot{
		Symbol:      s.client.Symbol(),
		Candles1H:   candles1H,
		Candles4H:   candles4H,
		Candles15M:  candles15M,
		Candles1D:   candles1D,
		OrderBook:   orderBook,
		RetrievedAt: time.Now().UTC(),
	}

	s.logger.Debug("市场数据快照获取完成",
		zap.String("symbol", snapshot.Symbol),
		zap.Time("retrieved_at", snapshot.RetrievedAt),
		zap.Int("candle_1h_count", len(snapshot.Candles1H)),
		zap.Int("candle_4h_count", len(snapshot.Candles4H)),
		zap.Int("order_book_bids", len(snapshot.OrderBook.Bids)),
		zap.Int("order_book_asks", len(snapshot.OrderBook.Asks)),
	)

	return snapshot, nil
}
