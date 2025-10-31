package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/multierr"
)

// Config 聚合了系统运行所需的全部配置项。
type Config struct {
	App       AppConfig           `mapstructure:"app"`
	Exchange  ExchangeConfig      `mapstructure:"exchange"`
	Trade     TradeExchangeConfig `mapstructure:"trade_exchange"`
	OpenAI    OpenAIConfig        `mapstructure:"openai"`
	Risk      RiskConfig          `mapstructure:"risk"`
	Execution ExecutionConfig     `mapstructure:"execution"`
	Database  DatabaseConfig      `mapstructure:"database"`
	Logging   LoggingConfig       `mapstructure:"logging"`
	Scheduler SchedulerConfig     `mapstructure:"scheduler"`
}

// AppConfig 控制应用级参数。
type AppConfig struct {
	Environment string `mapstructure:"environment"`
}

// ExchangeConfig 描述交易所连接信息。
type ExchangeConfig struct {
	Name       string      `mapstructure:"name"`
	Market     string      `mapstructure:"market"`
	APIKey     string      `mapstructure:"api_key"`
	APISecret  string      `mapstructure:"api_secret"`
	APIPass    string      `mapstructure:"api_password"`
	UseSandbox bool        `mapstructure:"use_sandbox"`
	Retry      RetryConfig `mapstructure:"retry"`
}

// TradeExchangeConfig 描述执行端交易所配置。
type TradeExchangeConfig struct {
	Name       string `mapstructure:"name"`
	Market     string `mapstructure:"market"`
	APIKey     string `mapstructure:"api_key"`
	APISecret  string `mapstructure:"api_secret"`
	APIPass    string `mapstructure:"api_password"`
	UseSandbox bool   `mapstructure:"use_sandbox"`
	Wallet     string `mapstructure:"wallet_address"`
	PrivateKey string `mapstructure:"private_key"`
}

// RetryConfig 统一控制重试机制。
type RetryConfig struct {
	MaxAttempts int           `mapstructure:"max_attempts"`
	MinDelay    time.Duration `mapstructure:"min_delay"`
	MaxDelay    time.Duration `mapstructure:"max_delay"`
}

// OpenAIConfig 描述大模型调用参数。
type OpenAIConfig struct {
	APIKey  string        `mapstructure:"api_key"`
	BaseURL string        `mapstructure:"base_url"`
	Model   string        `mapstructure:"model"`
	Timeout time.Duration `mapstructure:"timeout"`
}

// RiskConfig 管理风控参数。
type RiskConfig struct {
	MaxTradeRisk        float64 `mapstructure:"max_trade_risk"`
	MaxDailyLoss        float64 `mapstructure:"max_daily_loss"`
	MaxExposure         float64 `mapstructure:"max_exposure"`
	ConfidenceFullRisk  float64 `mapstructure:"confidence_full_risk"`
	ConfidenceHalfRisk  float64 `mapstructure:"confidence_half_risk"`
	DailyLossResetHour  int     `mapstructure:"daily_loss_reset_hour"`
	EnableDailyStopLoss bool    `mapstructure:"enable_daily_stop_loss"`
}

// ExecutionConfig 控制下单行为。
type ExecutionConfig struct {
	Slippage float64 `mapstructure:"slippage"`
}

// DatabaseConfig 管理数据库连接。
type DatabaseConfig struct {
	Path            string        `mapstructure:"path"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
	InMemory        bool          `mapstructure:"in_memory"`
}

// LoggingConfig 控制日志输出。
type LoggingConfig struct {
	Level            string   `mapstructure:"level"`
	Encoding         string   `mapstructure:"encoding"`
	Development      bool     `mapstructure:"development"`
	OutputPaths      []string `mapstructure:"output_paths"`
	ErrorOutputPaths []string `mapstructure:"error_output_paths"`
}

// SchedulerConfig 控制主循环节奏。
type SchedulerConfig struct {
	LoopInterval     time.Duration `mapstructure:"loop_interval"`
	DecisionInterval time.Duration `mapstructure:"decision_interval"`
	TrendInterval    time.Duration `mapstructure:"trend_interval"`
}

// Validate 对配置进行基本校验。
func (c *Config) Validate() error {
	var err error

	if c.App.Environment == "" {
		err = multierr.Append(err, errors.New("app.environment 不能为空"))
	}
	if c.Exchange.Name == "" {
		err = multierr.Append(err, errors.New("exchange.name 不能为空"))
	}
	if c.Exchange.Market == "" {
		err = multierr.Append(err, errors.New("exchange.market 不能为空"))
	}
	if c.Exchange.Retry.MaxAttempts <= 0 {
		err = multierr.Append(err, errors.New("exchange.retry.max_attempts 必须大于0"))
	}
	if c.Exchange.Retry.MinDelay <= 0 || c.Exchange.Retry.MaxDelay <= 0 {
		err = multierr.Append(err, errors.New("exchange.retry.delay 必须为正"))
	}
	if c.Exchange.Retry.MinDelay > c.Exchange.Retry.MaxDelay {
		err = multierr.Append(err, errors.New("exchange.retry.min_delay 不能大于 max_delay"))
	}
	if c.OpenAI.APIKey == "" {
		err = multierr.Append(err, errors.New("openai.api_key 不能为空"))
	}
	if c.OpenAI.Model == "" {
		err = multierr.Append(err, errors.New("openai.model 不能为空"))
	}
	if c.OpenAI.Timeout <= 0 {
		err = multierr.Append(err, errors.New("openai.timeout 必须大于0"))
	}
	if c.Risk.MaxTradeRisk <= 0 || c.Risk.MaxTradeRisk > 1 {
		err = multierr.Append(err, errors.New("risk.max_trade_risk 必须位于(0,1]"))
	}
	if c.Risk.MaxDailyLoss <= 0 || c.Risk.MaxDailyLoss > 1 {
		err = multierr.Append(err, errors.New("risk.max_daily_loss 必须位于(0,1]"))
	}
	if c.Risk.MaxExposure <= 0 || c.Risk.MaxExposure > 1 {
		err = multierr.Append(err, errors.New("risk.max_exposure 必须位于(0,1]"))
	}
	if c.Risk.ConfidenceFullRisk <= 0 || c.Risk.ConfidenceFullRisk > 1 {
		err = multierr.Append(err, errors.New("risk.confidence_full_risk 必须位于(0,1]"))
	}
	if c.Risk.ConfidenceHalfRisk <= 0 || c.Risk.ConfidenceHalfRisk > 1 {
		err = multierr.Append(err, errors.New("risk.confidence_half_risk 必须位于(0,1]"))
	}
	if c.Risk.ConfidenceHalfRisk >= c.Risk.ConfidenceFullRisk {
		err = multierr.Append(err, errors.New("risk.confidence_half_risk 必须小于 confidence_full_risk"))
	}
	if c.Risk.EnableDailyStopLoss && (c.Risk.DailyLossResetHour < 0 || c.Risk.DailyLossResetHour > 23) {
		err = multierr.Append(err, errors.New("risk.daily_loss_reset_hour 必须位于[0,23]"))
	}
	if c.Execution.Slippage < 0 || c.Execution.Slippage > 0.2 {
		err = multierr.Append(err, errors.New("execution.slippage 应位于[0,0.2]"))
	}
	if c.Trade.Name == "" {
		err = multierr.Append(err, errors.New("trade_exchange.name 不能为空"))
	}
	if c.Trade.Market == "" {
		err = multierr.Append(err, errors.New("trade_exchange.market 不能为空"))
	}
	if strings.EqualFold(c.Trade.Name, "hyperliquid") {
		if c.Trade.Wallet == "" || c.Trade.PrivateKey == "" {
			err = multierr.Append(err, errors.New("hyperliquid 交易需要配置 wallet_address 与 private_key"))
		}
	}
	if c.Database.Path == "" && !c.Database.InMemory {
		err = multierr.Append(err, errors.New("database.path 不能为空"))
	}
	if c.Database.MaxOpenConns <= 0 {
		err = multierr.Append(err, errors.New("database.max_open_conns 必须大于0"))
	}
	if c.Database.MaxIdleConns < 0 {
		err = multierr.Append(err, errors.New("database.max_idle_conns 不能为负"))
	}
	if c.Database.ConnMaxLifetime < 0 {
		err = multierr.Append(err, errors.New("database.conn_max_lifetime 不能为负"))
	}
	if c.Logging.Level == "" {
		err = multierr.Append(err, errors.New("logging.level 不能为空"))
	}
	if c.Logging.Encoding == "" {
		err = multierr.Append(err, errors.New("logging.encoding 不能为空"))
	}
	if len(c.Logging.OutputPaths) == 0 {
		err = multierr.Append(err, errors.New("logging.output_paths 至少包含一个输出目标"))
	}
	if len(c.Logging.ErrorOutputPaths) == 0 {
		err = multierr.Append(err, errors.New("logging.error_output_paths 至少包含一个输出目标"))
	}
	if c.Scheduler.LoopInterval <= 0 {
		err = multierr.Append(err, errors.New("scheduler.loop_interval 必须大于0"))
	}
	if c.Scheduler.DecisionInterval <= 0 {
		err = multierr.Append(err, errors.New("scheduler.decision_interval 必须大于0"))
	}
	if c.Scheduler.TrendInterval <= 0 {
		err = multierr.Append(err, errors.New("scheduler.trend_interval 必须大于0"))
	}
	if c.Scheduler.DecisionInterval < c.Scheduler.LoopInterval {
		err = multierr.Append(err, errors.New("scheduler.decision_interval 不应小于 loop_interval"))
	}
	if c.Scheduler.TrendInterval < c.Scheduler.DecisionInterval {
		err = multierr.Append(err, errors.New("scheduler.trend_interval 不应小于 decision_interval"))
	}
	if c.Trade.Name == "" {
		err = multierr.Append(err, errors.New("trade_exchange.name 不能为空"))
	}

	if err != nil {
		return fmt.Errorf("配置校验失败: %w", err)
	}

	return nil
}
