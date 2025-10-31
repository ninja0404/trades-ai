package backtest

import "time"

// Config 定义回测参数。
type Config struct {
	Symbol        string    // 交易对名称
	InitialEquity float64   // 初始净值
	StartTime     time.Time // 开始时间
	EndTime       time.Time // 结束时间
}

func (c *Config) normalize() Config {
	cfg := *c
	if cfg.InitialEquity <= 0 {
		cfg.InitialEquity = 10000
	}
	return cfg
}
