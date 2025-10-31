package risk

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"trades-ai/internal/config"
	"trades-ai/internal/store"
)

// Manager 负责执行风控评估。
type Manager struct {
	cfg     config.RiskConfig
	tracker *DailyTracker
	logger  *zap.Logger
}

// NewManager 创建风险管理器。
func NewManager(cfg config.RiskConfig, store *store.Store, logger *zap.Logger) (*Manager, error) {
	if store == nil {
		return nil, errors.New("risk: store 不能为空")
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	tracker, err := NewDailyTracker(store.DB(), cfg, logger)
	if err != nil {
		return nil, err
	}

	return &Manager{
		cfg:     cfg,
		tracker: tracker,
		logger:  logger,
	}, nil
}

// Evaluate 根据当前决策与账户状况给出风险评估结果。
func (m *Manager) Evaluate(ctx context.Context, input EvaluationInput) (EvaluationResult, error) {
	status, err := m.tracker.Update(ctx, input.Account.Timestamp, input.Account.Equity)
	if err != nil {
		return EvaluationResult{}, err
	}

	result := EvaluationResult{
		Status:      StatusDeny,
		DailyStatus: status,
		Notes:       make([]string, 0, 4),
	}

	stopLoss, stopErr := parseFloat(input.Decision.NewStopLoss)
	if stopErr != nil {
		result.Notes = append(result.Notes, fmt.Sprintf("AI 止损价解析失败: %v", stopErr))
	}
	takeProfit, takeErr := parseFloat(input.Decision.NewTakeProfit)
	if takeErr != nil {
		result.Notes = append(result.Notes, fmt.Sprintf("AI 止盈价解析失败: %v", takeErr))
	}

	result.RecommendedStopLoss = stopLoss
	result.RecommendedTakeProfit = takeProfit

	action := strings.ToUpper(strings.TrimSpace(input.Decision.Decision))
	posAction := strings.ToUpper(strings.TrimSpace(input.Decision.PositionAction))

	if action == "NEUTRAL" || posAction == "HOLD" {
		result.Notes = append(result.Notes, "模型建议观望/保持持仓，无需交易。")
		return result, nil
	}

	if isExitAction(action, posAction) {
		target := input.Account.CurrentExposurePercent
		switch {
		case posAction == "CLOSE" || action == "CLOSE":
			target = 0
			result.Notes = append(result.Notes, "执行平仓指令，将仓位降为 0。")
		case posAction == "REDUCE_50%" || action == "REDUCE":
			target = input.Account.CurrentExposurePercent * 0.5
			result.Notes = append(result.Notes, "执行减仓指令，将仓位减半。")
		default:
			target = 0
			result.Notes = append(result.Notes, "退出持仓。")
		}

		result.Status = StatusProceed
		result.TargetExposurePercent = target
		return result, nil
	}

	if status.Halted {
		result.Notes = append(result.Notes, "当日累计亏损已达到限制，停止开仓。")
		return result, nil
	}

	if input.Account.Equity <= 0 {
		result.Notes = append(result.Notes, "账户净值无效，无法评估仓位。")
		return result, nil
	}

	if input.MarketPrice <= 0 {
		result.Notes = append(result.Notes, "缺少有效的市价，无法计算仓位。")
		return result, nil
	}

	confidenceFactor, applied := m.confidenceFactor(input.Decision.Confidence)
	result.ConfidenceApplied = applied

	if confidenceFactor <= 0 {
		result.Notes = append(result.Notes, "模型信心度不足，放弃开仓。")
		return result, nil
	}

	direction := determineDirection(action, input.Position.Side)
	if direction == "" {
		direction = "LONG"
	}

	atr := input.Features.Volatility.ATRAbsolute
	finalStop := selectStop(direction, stopLoss, atr, input.MarketPrice)
	if finalStop <= 0 {
		result.Notes = append(result.Notes, "缺少有效止损，无法控制风险。")
		return result, nil
	}
	result.RecommendedStopLoss = finalStop

	stopDistance := computeStopDistance(direction, input.MarketPrice, finalStop)
	if stopDistance <= 0 {
		result.Notes = append(result.Notes, "止损位置不合理，无法计算风险敞口。")
		return result, nil
	}

	riskPerTrade := m.cfg.MaxTradeRisk * confidenceFactor
	riskAmount := input.Account.Equity * riskPerTrade
	result.RiskAmount = riskAmount

	if riskAmount <= 0 {
		result.Notes = append(result.Notes, "风险额度为零，禁止开仓。")
		return result, nil
	}

	targetExposure := riskPerTrade * (input.MarketPrice / stopDistance)
	if math.IsNaN(targetExposure) || math.IsInf(targetExposure, 0) {
		result.Notes = append(result.Notes, "无法计算目标仓位比例。")
		return result, nil
	}

	if direction == "SHORT" {
		targetExposure = -targetExposure
	}

	maxExp := m.cfg.MaxExposure
	if maxExp <= 0 {
		maxExp = 0.20
	}

	if math.Abs(targetExposure) > maxExp {
		result.Notes = append(result.Notes,
			fmt.Sprintf("按风险测算的仓位 %.2f%% 超过最大限制 %.2f%%，按上限执行。",
				math.Abs(targetExposure)*100, maxExp*100,
			),
		)
		targetExposure = math.Copysign(maxExp, targetExposure)
	}

	targetExposure = applyPositionAction(targetExposure, input.Account.CurrentExposurePercent, posAction, maxExp)

	if math.Abs(targetExposure) > maxExp {
		targetExposure = math.Copysign(maxExp, targetExposure)
	}

	if sameDirection(targetExposure, input.Account.CurrentExposurePercent) &&
		math.Abs(targetExposure) <= math.Abs(input.Account.CurrentExposurePercent)+1e-6 {
		result.Notes = append(result.Notes, "风险限制后未能提升仓位，放弃操作。")
		return result, nil
	}

	if math.Abs(targetExposure-input.Account.CurrentExposurePercent) < 1e-6 {
		result.Notes = append(result.Notes, "目标仓位与当前仓位几乎一致。")
		return result, nil
	}

	result.Status = StatusProceed
	result.TargetExposurePercent = targetExposure
	result.Notes = append(result.Notes,
		fmt.Sprintf("允许持仓比例提升至 %.2f%%，风险金额约为 %.2f。", targetExposure*100, riskAmount),
	)

	return result, nil
}

func (m *Manager) confidenceFactor(confidence float64) (float64, float64) {
	if confidence >= m.cfg.ConfidenceFullRisk {
		return 1.0, 1.0
	}
	if confidence >= m.cfg.ConfidenceHalfRisk {
		return 0.5, 0.5
	}
	return 0, 0
}

func parseFloat(value string) (float64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	return strconv.ParseFloat(value, 64)
}

func isExitAction(decision, positionAction string) bool {
	if decision == "CLOSE" || decision == "REDUCE" {
		return true
	}
	if positionAction == "CLOSE" || positionAction == "REDUCE_50%" {
		return true
	}
	return false
}

func determineDirection(decision, currentSide string) string {
	switch decision {
	case "LONG":
		return "LONG"
	case "SHORT":
		return "SHORT"
	case "ADD":
		if strings.ToUpper(currentSide) == "SHORT" {
			return "SHORT"
		}
		return "LONG"
	default:
		side := strings.ToUpper(currentSide)
		if side == "SHORT" || side == "LONG" {
			return side
		}
	}
	return ""
}

func selectStop(direction string, decisionStop, atr, price float64) float64 {
	if direction == "SHORT" {
		decisionCandidate := 0.0
		if decisionStop > price {
			decisionCandidate = decisionStop
		}
		atrCandidate := 0.0
		if atr > 0 {
			atrCandidate = price + 2*atr
		}

		switch {
		case decisionCandidate > 0 && atrCandidate > 0:
			return math.Min(decisionCandidate, atrCandidate)
		case decisionCandidate > 0:
			return decisionCandidate
		case atrCandidate > 0:
			return atrCandidate
		default:
			return 0
		}
	}

	decisionCandidate := 0.0
	if decisionStop > 0 && decisionStop < price {
		decisionCandidate = decisionStop
	}
	atrCandidate := 0.0
	if atr > 0 {
		if candidate := price - 2*atr; candidate > 0 {
			atrCandidate = candidate
		}
	}

	switch {
	case decisionCandidate > 0 && atrCandidate > 0:
		return math.Max(decisionCandidate, atrCandidate)
	case decisionCandidate > 0:
		return decisionCandidate
	case atrCandidate > 0:
		return atrCandidate
	default:
		return 0
	}
}

func computeStopDistance(direction string, price, stop float64) float64 {
	if direction == "SHORT" {
		return stop - price
	}
	return price - stop
}

func applyPositionAction(baseTarget, current float64, action string, maxExposure float64) float64 {
	switch action {
	case "HOLD", "":
		return baseTarget
	case "ADD_25%":
		sign := 1.0
		if baseTarget < 0 || (baseTarget == 0 && current < 0) {
			sign = -1.0
		}
		addTarget := current + 0.25*sign
		if math.Abs(addTarget) > maxExposure {
			addTarget = math.Copysign(maxExposure, addTarget)
		}
		if baseTarget == 0 {
			return addTarget
		}
		if sign > 0 {
			if addTarget > baseTarget {
				return baseTarget
			}
			return addTarget
		}
		if addTarget < baseTarget {
			return baseTarget
		}
		return addTarget
	default:
		return baseTarget
	}
}

func sameDirection(a, b float64) bool {
	if a == 0 || b == 0 {
		return false
	}
	return (a > 0 && b > 0) || (a < 0 && b < 0)
}
