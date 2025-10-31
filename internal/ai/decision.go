package ai

import (
	"errors"
	"fmt"
)

// Decision 表示大模型返回的交易指令。
type Decision struct {
	Decision       string  `json:"decision"`
	Confidence     float64 `json:"confidence"`
	Reasoning      string  `json:"reasoning"`
	PositionAction string  `json:"position_action"`
	NewStopLoss    string  `json:"new_stop_loss"`
	NewTakeProfit  string  `json:"new_take_profit"`
	RiskComment    string  `json:"risk_comment"`
}

var (
	validDecisions = map[string]struct{}{
		"LONG":    {},
		"SHORT":   {},
		"NEUTRAL": {},
		"CLOSE":   {},
		"REDUCE":  {},
		"ADD":     {},
	}
	validPositionActions = map[string]struct{}{
		"HOLD":       {},
		"CLOSE":      {},
		"REDUCE_50%": {},
		"ADD_25%":    {},
	}
)

// Validate 校验决策字段合法性。
func (d Decision) Validate() error {
	if _, ok := validDecisions[d.Decision]; !ok {
		return fmt.Errorf("decision 字段取值非法: %s", d.Decision)
	}

	if d.Confidence < 0 || d.Confidence > 1 {
		return fmt.Errorf("confidence 必须在 [0,1] 区间，目前为 %f", d.Confidence)
	}

	if _, ok := validPositionActions[d.PositionAction]; !ok {
		return fmt.Errorf("position_action 字段取值非法: %s", d.PositionAction)
	}

	if d.Reasoning == "" {
		return errors.New("reasoning 不能为空")
	}

	if d.NewStopLoss == "" {
		return errors.New("new_stop_loss 不能为空")
	}

	if d.NewTakeProfit == "" {
		return errors.New("new_take_profit 不能为空")
	}

	if d.RiskComment == "" {
		return errors.New("risk_comment 不能为空")
	}

	return nil
}
