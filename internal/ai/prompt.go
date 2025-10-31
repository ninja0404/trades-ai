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
你是一个专业的加密货币量化交易员。你的任务是根据提供的市场数据特征，给出明确的交易决策。

当前市场数据：
{{ .FeaturesJSON }}

当前持仓状况：
- 持仓方向: {{ .Position.Side }}
- 仓位大小: {{ printf "%.2f" .Position.SizePercent }}% of portfolio
- 入场价格: {{ .Position.EntryPrice }}
- 当前盈亏: {{ printf "%.2f" .Position.UnrealizedPnlPercent }}%
- 持仓时间: {{ .Position.PositionAgeHours }}小时
- 当前止损: {{ .Position.StopLoss }}
- 当前止盈: {{ .Position.TakeProfit }}

决策框架（考虑持仓状态）：
1. 趋势判断：首先分析市场整体趋势方向（多头/空头/震荡）
2. 动量确认：检查当前动能是否支持趋势延续
3. 风险评估：识别潜在的反转信号和风险点
4. 如果当前无持仓：
   - 分析市场趋势和动量，寻找高胜率入场点
   - 评估风险收益比，只在明显机会时入场
5. 如果当前有持仓：
   - 评估持仓的健康度（盈亏、时间、信号变化）
   - 决定是否继续持有、加仓、减仓或平仓
   - 特别注意止损和止盈的调整
6. 风险管理：
   - 单次交易风险不超过总资金的1%
   - 总持仓不超过总资金的20%
   - 避免过度交易和报复性交易
7. 最终决策：基于以上分析给出明确指令

决策原则：
- 宁可错过，不要做错。当信号不明确时，选择NEUTRAL
- 遵循趋势，不逆势操作
- 关注成交量确认信号
- 风险控制是第一优先级
- 决策必须有明确的理由支撑

特殊情形处理：
- 如果持仓已盈利且信号开始反转，考虑部分或全部止盈
- 如果持仓亏损且达到心理止损位，坚决平仓
- 如果市场出现重大变化，重新评估整个策略

请严格按照以下JSON格式输出，只返回JSON对象，不要有其他内容：
{
  "decision": "LONG|SHORT|NEUTRAL|CLOSE|REDUCE|ADD",
  "confidence": 0.0-1.0,
  "reasoning": "包含持仓管理的决策理由",
  "position_action": "HOLD|CLOSE|REDUCE_50%|ADD_25%",
  "new_stop_loss": "调整后的止损价",
  "new_take_profit": "调整后的止盈价",
  "risk_comment": "特别风险提示"
}
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
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("渲染提示词失败: %w", err)
	}

	return buf.String(), nil
}
