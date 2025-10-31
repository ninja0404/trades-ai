package log

import (
	"fmt"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"trades-ai/internal/config"
)

// NewLogger 根据配置创建 zap.Logger。
func NewLogger(cfg config.LoggingConfig) (*zap.Logger, error) {
	level := zapcore.InfoLevel
	if err := level.Set(strings.ToLower(cfg.Level)); err != nil {
		return nil, fmt.Errorf("解析日志级别失败: %w", err)
	}

	if len(cfg.OutputPaths) == 0 {
		cfg.OutputPaths = []string{"stdout"}
	}
	if len(cfg.ErrorOutputPaths) == 0 {
		cfg.ErrorOutputPaths = []string{"stderr"}
	}

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeDuration = zapcore.StringDurationEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	encoderConfig.TimeKey = "ts"
	encoderConfig.NameKey = "logger"
	encoderConfig.CallerKey = "caller"

	zapCfg := zap.Config{
		Level:       zap.NewAtomicLevelAt(level),
		Development: cfg.Development,
		Encoding:    cfg.Encoding,
		EncoderConfig: zapcore.EncoderConfig{
			MessageKey:     encoderConfig.MessageKey,
			LevelKey:       encoderConfig.LevelKey,
			TimeKey:        encoderConfig.TimeKey,
			NameKey:        encoderConfig.NameKey,
			CallerKey:      encoderConfig.CallerKey,
			FunctionKey:    zapcore.OmitKey,
			StacktraceKey:  encoderConfig.StacktraceKey,
			LineEnding:     encoderConfig.LineEnding,
			EncodeLevel:    encoderConfig.EncodeLevel,
			EncodeTime:     encoderConfig.EncodeTime,
			EncodeDuration: encoderConfig.EncodeDuration,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		},
		OutputPaths:      cfg.OutputPaths,
		ErrorOutputPaths: cfg.ErrorOutputPaths,
		InitialFields:    map[string]interface{}{"service": "trades-ai"},
	}

	logger, err := zapCfg.Build(zap.AddCaller(), zap.AddCallerSkip(1))
	if err != nil {
		return nil, fmt.Errorf("创建日志实例失败: %w", err)
	}

	return logger, nil
}
