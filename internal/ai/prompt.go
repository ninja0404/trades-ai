package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"trades-ai/internal/feature"
	"trades-ai/internal/position"
)

// AssetInput 表示提示词中的单个资产数据。
type AssetInput struct {
	Symbol   string
	Features feature.FeatureSet
	Position position.Summary
}

// AccountSnapshot 描述账户资金与绩效概况。
type AccountSnapshot struct {
	Equity           float64
	Balance          float64
	FreeBalance      float64
	Withdrawable     float64
	MarginUsed       float64
	TotalNotional    float64
	UnrealizedPnL    float64
	CrossEquity      float64
	CrossMarginUsed  float64
	NetExposurePct   float64
	GrossExposurePct float64
}

type promptAsset struct {
	Symbol       string
	FeaturesJSON string
	PositionText string
}

type PromptContext struct {
	Assets       []promptAsset
	HoldingsJSON string
	AccountText  string
	AccountJSON  string
}

const decisionTemplate = `
你是一个专业的加密货币量化交易员。请基于多资产行情与持仓信息，在遵循严格风险约束的前提下制定针对每个资产的目标仓位和保护策略，尽量让账户收益最大化。

说明：
- Trend/Momentum/Volatility/MarketStructure/MarketState 均来源于 1 小时数据；HigherTimeframeTrend 采用 4 小时结果，多周期动量指标在 JSON 中明确标注 15m/1h/4h/1d。
- 请综合考虑相关性、风险敞口与账户整体约束，必要时可以选择 OBSERVE 保持观望。

{{ range .Assets }}=== 资产: {{ .Symbol }} ===
市场特征 (JSON):
{{ .FeaturesJSON }}

当前持仓：
{{ .PositionText }}

{{ end }}=== 持仓 JSON 汇总 ===
{{ .HoldingsJSON }}

=== 账户概览 ===
{{ .AccountText }}

账户概览 JSON:
{{ .AccountJSON }}

制定决策时请遵循：
1. 先判断趋势与动量，确认是否存在高胜率方向；
2. 结合风险与仓位健康度，决定是保持、减仓、加仓还是反手；
3. 做出明确的仓位目标（占净值比例），并给出新的止损/止盈；
4. 保守处理不确定情形，必要时保持或回到空仓；
5. 单笔最大风险不超过净值的 1%，总仓位不超过 20%。

请严格输出唯一的 JSON 对象，格式如下：
{
  "decisions": [
    {
      "symbol": "BTC",                                 // 资产代号，需与上方资产段落一致
      "intent": "OPEN|ADJUST|CLOSE|HEDGE|OBSERVE",       // OBSERVE 表示仅观望，不执行交易
      "direction": "LONG|SHORT|FLAT|AUTO",              // 目标方向，FLAT 代表空仓，AUTO 允许结合分析自行推断
      "target_exposure_pct": 0.0-1.0,                     // 目标仓位绝对占比，CLOSE 时应为 0
      "adjustment_pct": -1.0-1.0,                         // （可选）相对当前仓位的调整比例；无调整填 0
      "confidence": 0.0-1.0,                              // 决策信心度
      "reasoning": "...",                                // 支撑结论的关键理由
      "order_preference": "MARKET|LIMIT|AUTO",           // （可选）下单方式偏好，默认 AUTO 或留空
      "new_stop_loss": "...",                           // OPEN/ADJUST/HEDGE 必填；CLOSE/OBSERVE 可留空或返回 ""
      "new_take_profit": "...",                         // OPEN/ADJUST/HEDGE 必填；CLOSE/OBSERVE 可留空或返回 ""
      "risk_comment": "..."                             // （可选）特别风险提示，可留空
    }
  ]
}

注意事项：
- target_exposure_pct 表示相对账户净值的绝对仓位比例，请勿超过 1。
- 若 intent=CLOSE，请使用 direction=FLAT、target_exposure_pct=0、adjustment_pct=0，并可清空止损/止盈字段。
- 若 intent=OBSERVE，请保持方向与仓位不变，可返回 FLAT/0 或沿用现状；止损/止盈可为空字符串。
- adjustment_pct 为微调项；若只关心最终目标，请使用 0。
- 所有价格字段请返回可解析的字符串（十进制数值）。
`

var tmpl = template.Must(template.New("decision").Parse(decisionTemplate))

// BuildPrompt 将多资产特征、仓位与账户信息渲染成提示词字符串。
func BuildPrompt(assets []AssetInput, account AccountSnapshot) (string, error) {
	if len(assets) == 0 {
		return "", fmt.Errorf("缺少资产输入，无法构建提示词")
	}

	promptAssets := make([]promptAsset, 0, len(assets))
	holdings := make([]map[string]interface{}, 0, len(assets))

	for _, asset := range assets {
		featuresJSON, err := json.MarshalIndent(asset.Features, "", "  ")
		if err != nil {
			return "", fmt.Errorf("序列化特征失败 (%s): %w", asset.Symbol, err)
		}

		positionText := fmt.Sprintf(
			"方向: %s | 仓位占比: %.2f%% | 入场价: %.4f | 未实现盈亏: %.2f%% | 止损: %.4f | 止盈: %.4f",
			valueOrDefault(asset.Position.Side, "无持仓"),
			asset.Position.SizePercent,
			asset.Position.EntryPrice,
			asset.Position.UnrealizedPnlPercent,
			asset.Position.StopLoss,
			asset.Position.TakeProfit,
		)

		promptAssets = append(promptAssets, promptAsset{
			Symbol:       asset.Symbol,
			FeaturesJSON: string(featuresJSON),
			PositionText: positionText,
		})

		holdings = append(holdings, map[string]interface{}{
			"symbol":                 asset.Symbol,
			"side":                   valueOrDefault(asset.Position.Side, "FLAT"),
			"size_percent":           asset.Position.SizePercent,
			"entry_price":            asset.Position.EntryPrice,
			"unrealized_pnl_percent": asset.Position.UnrealizedPnlPercent,
			"position_age_hours":     asset.Position.PositionAgeHours,
			"stop_loss":              asset.Position.StopLoss,
			"take_profit":            asset.Position.TakeProfit,
		})
	}

	holdingsJSON, err := json.MarshalIndent(holdings, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化持仓失败: %w", err)
	}

	accountJSON, err := json.MarshalIndent(account, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化账户概况失败: %w", err)
	}

	accountText := fmt.Sprintf(
		"净值 %.2f | 账户余额 %.2f | 可用 %.2f | 可提 %.2f | 已用保证金 %.2f | 总名义仓位 %.2f | 未实现盈亏 %.2f | 净敞口 %.2f%% | 总敞口 %.2f%%",
		account.Equity,
		account.Balance,
		account.FreeBalance,
		account.Withdrawable,
		account.MarginUsed,
		account.TotalNotional,
		account.UnrealizedPnL,
		account.NetExposurePct,
		account.GrossExposurePct,
	)

	ctx := PromptContext{
		Assets:       promptAssets,
		HoldingsJSON: string(holdingsJSON),
		AccountText:  accountText,
		AccountJSON:  string(accountJSON),
	}

	var buf bytes.Buffer
	if err = tmpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("渲染提示词失败: %w", err)
	}

	return buf.String(), nil
}

func valueOrDefault(val, fallback string) string {
	if strings.TrimSpace(val) == "" {
		return fallback
	}
	return val
}
