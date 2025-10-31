package exchange

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	ccxt "github.com/ccxt/ccxt/go/v4"
	"go.uber.org/zap"

	"trades-ai/internal/config"
)

// Client 负责与交易所交互并实现重试机制。
type Client struct {
	cfg      config.ExchangeConfig
	logger   *zap.Logger
	exchange *ccxt.Binanceusdm
	symbol   string

	marketsMu     sync.Mutex
	marketsLoaded bool
}

// NewClient 构造 Binance USDⓈ-M 客户端。
func NewClient(cfg config.ExchangeConfig, symbol string, logger *zap.Logger) (*Client, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	userConfig := map[string]interface{}{
		"enableRateLimit": true,
		"options": map[string]interface{}{
			"adjustForTimeDifference": true,
			"defaultType":             "future",
		},
	}

	if cfg.APIKey != "" {
		userConfig["apiKey"] = cfg.APIKey
	}
	if cfg.APISecret != "" {
		userConfig["secret"] = cfg.APISecret
	}
	if cfg.APIPass != "" {
		userConfig["password"] = cfg.APIPass
	}

	ex := ccxt.NewBinanceusdm(userConfig)
	if cfg.UseSandbox {
		ex.SetSandboxMode(true)
	}

	return &Client{
		cfg:      cfg,
		logger:   logger,
		exchange: ex,
		symbol:   symbol,
	}, nil
}

// Symbol 返回交易对符号。
func (c *Client) Symbol() string {
	return c.symbol
}

// Raw 返回底层 ccxt 客户端。
func (c *Client) Raw() *ccxt.Binanceusdm {
	return c.exchange
}

// FetchCandles 获取指定周期的K线数据。
func (c *Client) FetchCandles(ctx context.Context, timeframe string, limit int64) ([]Candle, error) {
	if limit <= 0 {
		limit = 1
	}

	var raw []ccxt.OHLCV

	err := c.callWithRetry(ctx, fmt.Sprintf("fetch_ohlcv_%s", timeframe), func() error {
		if err := c.ensureMarketsLoaded(ctx); err != nil {
			return err
		}

		result, err := c.exchange.FetchOHLCV(
			c.symbol,
			ccxt.WithFetchOHLCVTimeframe(timeframe),
			ccxt.WithFetchOHLCVLimit(limit),
		)
		if err != nil {
			return err
		}

		raw = result
		return nil
	})
	if err != nil {
		return nil, err
	}

	candles := make([]Candle, 0, len(raw))
	for _, item := range raw {
		ts := time.UnixMilli(item.Timestamp).UTC()
		candles = append(candles, Candle{
			Timestamp: ts,
			Open:      item.Open,
			High:      item.High,
			Low:       item.Low,
			Close:     item.Close,
			Volume:    item.Volume,
		})
	}

	return candles, nil
}

// FetchOrderBook 获取订单簿快照。
func (c *Client) FetchOrderBook(ctx context.Context, depth int64) (OrderBookSnapshot, error) {
	if depth <= 0 {
		depth = 50
	}

	var raw ccxt.OrderBook
	err := c.callWithRetry(ctx, "fetch_order_book", func() error {
		if err := c.ensureMarketsLoaded(ctx); err != nil {
			return err
		}

		orderBook, err := c.exchange.FetchOrderBook(
			c.symbol,
			ccxt.WithFetchOrderBookLimit(depth),
		)
		if err != nil {
			return err
		}

		raw = orderBook
		return nil
	})
	if err != nil {
		return OrderBookSnapshot{}, err
	}

	return convertOrderBook(c.symbol, raw), nil
}

func (c *Client) ensureMarketsLoaded(ctx context.Context) error {
	if c.marketsLoaded {
		return nil
	}

	c.marketsMu.Lock()
	defer c.marketsMu.Unlock()

	if c.marketsLoaded {
		return nil
	}

	loadErr := c.callWithRetry(ctx, "load_markets", func() error {
		_, err := c.exchange.LoadMarkets()
		return err
	})
	if loadErr != nil {
		return loadErr
	}

	c.marketsLoaded = true
	c.logger.Info("已完成市场元数据加载", zap.String("symbol", c.symbol))
	return nil
}

func (c *Client) callWithRetry(ctx context.Context, operation string, fn func() error) error {
	attempt := 0
	delay := c.cfg.Retry.MinDelay
	if delay <= 0 {
		delay = 500 * time.Millisecond
	}
	maxDelay := c.cfg.Retry.MaxDelay
	if maxDelay <= 0 {
		maxDelay = 5 * time.Second
	}

	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		attempt++
		start := time.Now()
		err := fn()
		duration := time.Since(start)
		if err == nil {
			if attempt > 1 {
				c.logger.Info("交易所调用重试后成功",
					zap.String("operation", operation),
					zap.Int("attempts", attempt),
					zap.Duration("latency", duration),
				)
			}
			return nil
		}

		normalizedErr, retry := c.classifyError(err)

		if errors.Is(normalizedErr, ErrMaintenance) {
			c.logger.Warn("交易所维护中",
				zap.String("operation", operation),
				zap.Error(normalizedErr),
			)
			return normalizedErr
		}

		if !retry || attempt >= c.cfg.Retry.MaxAttempts {
			c.logger.Error("交易所调用失败",
				zap.String("operation", operation),
				zap.Int("attempts", attempt),
				zap.Duration("latency", duration),
				zap.Error(normalizedErr),
			)
			return normalizedErr
		}

		wait := delay
		if wait > maxDelay {
			wait = maxDelay
		}

		c.logger.Warn("交易所调用失败，等待重试",
			zap.String("operation", operation),
			zap.Int("attempt", attempt),
			zap.Duration("wait", wait),
			zap.Error(normalizedErr),
		)

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}

		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

func (c *Client) classifyError(err error) (error, bool) {
	if err == nil {
		return nil, false
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err, false
	}

	var ccxtErr *ccxt.Error
	if errors.As(err, &ccxtErr) {
		switch ccxtErr.Type {
		case ccxt.NetworkErrorErrType,
			ccxt.RequestTimeoutErrType,
			ccxt.ExchangeNotAvailableErrType,
			ccxt.RateLimitExceededErrType,
			ccxt.DDoSProtectionErrType,
			ccxt.BadResponseErrType,
			ccxt.NullResponseErrType:
			return err, true
		case ccxt.OnMaintenanceErrType:
			message := strings.TrimSpace(ccxtErr.Message)
			if message == "" {
				message = "exchange under maintenance"
			}
			return fmt.Errorf("%w: %s", ErrMaintenance, message), false
		default:
			return err, false
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return err, true
	}

	return err, false
}

func convertOrderBook(symbol string, ob ccxt.OrderBook) OrderBookSnapshot {
	bids := make([]OrderBookLevel, 0, len(ob.Bids))
	for _, level := range ob.Bids {
		if len(level) < 2 {
			continue
		}
		bids = append(bids, OrderBookLevel{
			Price:  level[0],
			Amount: level[1],
		})
	}

	asks := make([]OrderBookLevel, 0, len(ob.Asks))
	for _, level := range ob.Asks {
		if len(level) < 2 {
			continue
		}
		asks = append(asks, OrderBookLevel{
			Price:  level[0],
			Amount: level[1],
		})
	}

	var ts time.Time
	if ob.Timestamp != nil {
		ts = time.UnixMilli(*ob.Timestamp).UTC()
	} else {
		ts = time.Now().UTC()
	}

	var nonce int64
	if ob.Nonce != nil {
		nonce = *ob.Nonce
	}

	return OrderBookSnapshot{
		Symbol:    symbol,
		Bids:      bids,
		Asks:      asks,
		Timestamp: ts,
		Nonce:     nonce,
	}
}
