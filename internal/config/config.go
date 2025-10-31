package config

import (
	"errors"
	"fmt"
	"strings"

	mapstructure "github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

const (
	defaultConfigPath = "configs/config.yaml"
	envPrefix         = "trades"
)

// Load 从配置文件与环境变量读取配置。
func Load(path string) (Config, error) {
	v := viper.New()
	setDefaults(v)

	if err := loadFile(v, path); err != nil {
		return Config{}, err
	}

	bindEnv(v)

	var cfg Config
	if err := v.Unmarshal(&cfg, decodeHook()); err != nil {
		return Config{}, fmt.Errorf("解析配置失败: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("app.environment", "development")

	v.SetDefault("exchange.name", "binanceusdm")
	v.SetDefault("exchange.markets", []string{"BTC/USDT:USDT"})
	v.SetDefault("exchange.use_sandbox", false)
	v.SetDefault("exchange.retry.max_attempts", 5)
	v.SetDefault("exchange.retry.min_delay", "500ms")
	v.SetDefault("exchange.retry.max_delay", "5s")

	v.SetDefault("trade_exchange.name", "hyperliquid")
	v.SetDefault("trade_exchange.markets", []string{"BTC/USDC"})
	v.SetDefault("trade_exchange.api_key", "")
	v.SetDefault("trade_exchange.api_secret", "")
	v.SetDefault("trade_exchange.api_password", "")
	v.SetDefault("trade_exchange.use_sandbox", false)
	v.SetDefault("trade_exchange.wallet_address", "")
	v.SetDefault("trade_exchange.private_key", "")

	v.SetDefault("openai.base_url", "https://api.openai.com/v1")
	v.SetDefault("openai.model", "gpt-4.1")
	v.SetDefault("openai.timeout", "15s")

	v.SetDefault("risk.max_trade_risk", 0.01)
	v.SetDefault("risk.max_daily_loss", 0.03)
	v.SetDefault("risk.max_exposure", 0.20)
	v.SetDefault("risk.confidence_full_risk", 0.80)
	v.SetDefault("risk.confidence_half_risk", 0.60)
	v.SetDefault("risk.daily_loss_reset_hour", 0)
	v.SetDefault("risk.enable_daily_stop_loss", true)

	v.SetDefault("execution.slippage", 0.01)

	v.SetDefault("database.path", "data/trades_ai.db")
	v.SetDefault("database.max_open_conns", 4)
	v.SetDefault("database.max_idle_conns", 4)
	v.SetDefault("database.conn_max_lifetime", "1h")
	v.SetDefault("database.in_memory", false)

	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.encoding", "console")
	v.SetDefault("logging.development", true)
	v.SetDefault("logging.output_paths", []string{"stdout"})
	v.SetDefault("logging.error_output_paths", []string{"stderr"})

	v.SetDefault("scheduler.loop_interval", "5m")
	v.SetDefault("scheduler.decision_interval", "1h")
	v.SetDefault("scheduler.trend_interval", "4h")
}

func decodeHook() viper.DecoderConfigOption {
	return func(dc *mapstructure.DecoderConfig) {
		dc.TagName = "mapstructure"
		dc.DecodeHook = mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
		)
	}
}

func loadFile(v *viper.Viper, path string) error {
	configPath := strings.TrimSpace(path)
	if configPath == "" {
		configPath = defaultConfigPath
	}

	v.SetConfigFile(configPath)

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) {
			return fmt.Errorf("未找到配置文件 %s: %w", configPath, err)
		}
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	return nil
}

func bindEnv(v *viper.Viper) {
	v.SetEnvPrefix(envPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
}
