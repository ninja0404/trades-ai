package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
	"go.uber.org/zap"

	"trades-ai/internal/config"
)

// Client 封装 OpenAI 调用逻辑。
type Client struct {
	cfg    config.OpenAIConfig
	logger *zap.Logger
	sdk    *openai.Client
}

// NewClient 使用给定配置创建 AI 客户端。
func NewClient(cfg config.OpenAIConfig, logger *zap.Logger) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("openai api_key 不能为空")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	config := openai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		config.BaseURL = cfg.BaseURL
	}

	httpClient := &http.Client{
		Timeout: cfg.Timeout + 5*time.Second,
	}
	config.HTTPClient = httpClient
	client := openai.NewClientWithConfig(config)

	return &Client{
		cfg:    cfg,
		logger: logger,
		sdk:    client,
	}, nil
}

// GenerateDecision 根据特征与仓位信息获取模型决策。
func (c *Client) GenerateDecision(ctx context.Context, assets []AssetInput, account AccountSnapshot) ([]Decision, error) {
	if c.cfg.Model == "" {
		return nil, errors.New("openai model 不能为空")
	}

	prompt, err := BuildPrompt(assets, account)
	if err != nil {
		return nil, err
	}

	response, err := c.sdk.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: c.cfg.Model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt,
			},
		},
		Temperature: 0,
	})
	if err != nil {
		c.logger.Error("调用OpenAI失败", zap.Error(err))
		return nil, fmt.Errorf("调用OpenAI失败: %w", err)
	}

	if len(response.Choices) == 0 {
		return nil, errors.New("OpenAI 返回结果为空")
	}

	rawContent := strings.TrimSpace(response.Choices[0].Message.Content)
	if rawContent == "" {
		return nil, errors.New("OpenAI 返回内容为空")
	}

	decisions, err := parseDecision(rawContent)
	if err != nil {
		c.logger.Error("解析模型决策失败",
			zap.Error(err),
			zap.String("raw_content", rawContent),
		)
		return nil, err
	}

	for _, decision := range decisions {
		if err = decision.Validate(); err != nil {
			return nil, err
		}
		c.logger.Info("AI 决策生成成功",
			zap.String("asset", decision.Symbol),
			zap.String("intent", decision.Intent),
			zap.String("direction", decision.Direction),
			zap.Float64("target_exposure_pct", decision.TargetExposurePct),
			zap.Float64("confidence", decision.Confidence),
		)
	}

	return decisions, nil
}

func parseDecision(content string) ([]Decision, error) {
	jsonPayload, err := extractJSON(content)
	if err != nil {
		return nil, err
	}

	var envelope DecisionEnvelope
	if err = json.Unmarshal(jsonPayload, &envelope); err != nil {
		return nil, fmt.Errorf("解析决策JSON失败: %w", err)
	}
	if len(envelope.Decisions) == 0 {
		return nil, errors.New("AI 返回的 decisions 为空")
	}
	return envelope.Decisions, nil
}

func extractJSON(content string) ([]byte, error) {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")

	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("模型输出未找到有效JSON: %s", content)
	}

	return []byte(content[start : end+1]), nil
}
