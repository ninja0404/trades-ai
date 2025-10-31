package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"text/template"

	"trades-ai/internal/feature"
	"trades-ai/internal/position"
)

const decisionTemplate = `
你是一个专业的加密货币量化交易员。你的任务是根据提供的市场数据特征，在遵循严格风险约束的前提下给出新的目标仓位与风险控制建议。

当前市场数据：
{{ .FeaturesJSON }}

当前持仓状况：
- 持仓方向: {{ .Position.Side }}
- 仓位大小: {{ printf "%.2f" .Position.SizePercent }}% of portfolio
- 入场价格: {{ .Position.EntryPrice }}
- 当前盈亏: {{ printf "%.2f" .Position.UnrealizedPnlPercent }}%
- 持仓时间: {{ .Position.PositionAgeHours }} 小时
- 当前止损: {{ .Position.StopLoss }}
- 当前止盈: {{ .Position.TakeProfit }}

制定决策时请遵循：
1. 先判断趋势与动量，确认是否存在高胜率方向；
2. 结合风险与仓位健康度，决定是保持、减仓、加仓还是反手；
3. 做出明确的仓位目标（占净值比例），并给出新的止损/止盈；
4. 保守处理不确定情形，必要时保持或回到空仓；
5. 单笔最大风险不超过净值的 1%，总仓位不超过 20%。

请严格输出唯一的 JSON 对象，格式如下：
{
  "intent": "OPEN|ADJUST|CLOSE|HEDGE",                    // OPEN: 建立/增加仓位, ADJUST: 调整仓位, CLOSE: 清仓, HEDGE: 对冲或反手
  "direction": "LONG|SHORT|FLAT|AUTO",                  // 目标方向，FLAT 代表空仓，AUTO 允许根据分析或现有仓位自行推断
  "target_exposure_pct": 0.0-1.0,                         // 目标仓位绝对占比（例如 0.25 代表 25% 净值），若 intent=CLOSE 请填 0
  "adjustment_pct": -1.0-1.0,                             // （可选）相对当前仓位的调整幅度（正值增加仓位，负值减仓，不调整请填 0）
  "confidence": 0.0-1.0,                                  // 决策信心度
  "reasoning": "...",                                    // 支撑结论的关键理由
  "order_preference": "MARKET|LIMIT|AUTO",               // 下单方式偏好，若无特别要求可返回 "AUTO"
  "new_stop_loss": "...",                               // 新的止损价格（必须给出数值字符串）
  "new_take_profit": "...",                             // 新的止盈价格（必须给出数值字符串）
  "risk_comment": "..."                                 // 特别风险提示或注意事项
}

注意事项：
- target_exposure_pct 表示绝对仓位占比，请勿返回超过 1 的数值。
- 若需要平仓，请将 intent 设置为 CLOSE，并将 direction 设为 FLAT、target_exposure_pct=0、adjustment_pct=0。
- adjustment_pct 用于微调当前仓位。当给定非 0 时，表示在当前仓位基础上增减对应比例；若仅关心最终目标，可将其填为 0。
- 所有字段均需填写；止损和止盈必须为可解析的价格字符串。
`

var tmpl = template.Must(template.New("decision").Parse(decisionTemplate))

// PromptContext 用于渲染提示词。
type PromptContext struct {
	Features     feature.FeatureSet
	Position     position.Summary
	FeaturesJSON string
}

// BuildPrompt 将特征与仓位信息渲染成提示词字符串。
func BuildPrompt(features feature.FeatureSet, pos position.Summary) (string, error) {
	featuresJSONBytes, err := json.MarshalIndent(features, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化特征失败: %w", err)
	}

	ctx := PromptContext{
		Features:     features,
		Position:     pos,
		FeaturesJSON: string(featuresJSONBytes),
	}

	var buf bytes.Buffer
	if err = tmpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("渲染提示词失败: %w", err)
	}

	return buf.String(), nil
}
