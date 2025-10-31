package ai

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Decision 表示大模型返回的交易指令。
type Decision struct {
	Symbol            string  `json:"symbol"`
	Intent            string  `json:"intent"`
	Direction         string  `json:"direction"`
	TargetExposurePct float64 `json:"target_exposure_pct"`
	AdjustmentPct     float64 `json:"adjustment_pct"`
	Confidence        float64 `json:"confidence"`
	Reasoning         string  `json:"reasoning"`
	OrderPreference   string  `json:"order_preference"`
	NewStopLoss       string  `json:"new_stop_loss"`
	NewTakeProfit     string  `json:"new_take_profit"`
	RiskComment       string  `json:"risk_comment"`
}

var (
	validIntents = map[string]struct{}{
		"OPEN":    {},
		"ADJUST":  {},
		"CLOSE":   {},
		"HEDGE":   {},
		"OBSERVE": {},
	}
	validDirections = map[string]struct{}{
		"LONG":  {},
		"SHORT": {},
		"FLAT":  {},
		"AUTO":  {},
	}
	validOrderPreferences = map[string]struct{}{
		"MARKET": {},
		"LIMIT":  {},
		"AUTO":   {},
	}
)

// Validate 校验决策字段合法性。
func (d Decision) Validate() error {
	if strings.TrimSpace(d.Symbol) == "" {
		return errors.New("symbol 不能为空")
	}
	intent := strings.ToUpper(strings.TrimSpace(d.Intent))
	if intent == "" {
		return errors.New("intent 不能为空")
	}
	if _, ok := validIntents[intent]; !ok {
		return fmt.Errorf("intent 字段取值非法: %s", d.Intent)
	}

	direction := strings.ToUpper(strings.TrimSpace(d.Direction))
	if direction == "" {
		return errors.New("direction 不能为空")
	}
	if _, ok := validDirections[direction]; !ok {
		return fmt.Errorf("direction 字段取值非法: %s", d.Direction)
	}

	if d.TargetExposurePct < 0 || d.TargetExposurePct > 1 {
		return fmt.Errorf("target_exposure_pct 必须位于 [0,1]，当前为 %f", d.TargetExposurePct)
	}
	if math.Abs(d.AdjustmentPct) > 1 {
		return fmt.Errorf("adjustment_pct 必须位于 [-1,1]，当前为 %f", d.AdjustmentPct)
	}

	if d.Confidence < 0 || d.Confidence > 1 {
		return fmt.Errorf("confidence 必须在 [0,1] 区间，目前为 %f", d.Confidence)
	}

	if strings.TrimSpace(d.Reasoning) == "" {
		return errors.New("reasoning 不能为空")
	}

	orderPref := strings.ToUpper(strings.TrimSpace(d.OrderPreference))
	if orderPref != "" {
		if _, ok := validOrderPreferences[orderPref]; !ok {
			return fmt.Errorf("order_preference 字段取值非法: %s", d.OrderPreference)
		}
	}

	requireStops := intent == "OPEN" || intent == "ADJUST" || intent == "HEDGE"

	if requireStops {
		if strings.TrimSpace(d.NewStopLoss) == "" {
			return errors.New("new_stop_loss 不能为空 (OPEN/ADJUST/HEDGE)")
		}
		if strings.TrimSpace(d.NewTakeProfit) == "" {
			return errors.New("new_take_profit 不能为空 (OPEN/ADJUST/HEDGE)")
		}
	}

	return nil
}

// DecisionEnvelope 用于解析多资产决策列表。
type DecisionEnvelope struct {
	Decisions []Decision `json:"decisions"`
}
