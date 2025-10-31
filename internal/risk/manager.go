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

const exposureEpsilon = 1e-6

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
		Notes:       make([]string, 0, 6),
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

	intent := strings.ToUpper(strings.TrimSpace(input.Decision.Intent))
	if intent == "" {
		intent = "ADJUST"
	}
	direction := strings.ToUpper(strings.TrimSpace(input.Decision.Direction))
	if direction == "" {
		direction = "AUTO"
	}

	if orderPref := strings.ToUpper(strings.TrimSpace(input.Decision.OrderPreference)); orderPref != "" {
		result.Notes = append(result.Notes, fmt.Sprintf("模型下单偏好: %s", orderPref))
	}

	desiredExposure := deriveDesiredExposure(intent, direction, input.Decision.TargetExposurePct, input.Decision.AdjustmentPct, input.Account.CurrentExposurePercent, input.Position.Side)
	result.Notes = append(result.Notes,
		fmt.Sprintf("AI 期望仓位 %.2f%% (方向 %s)，调整幅度 %.2f%%。",
			desiredExposure*100,
			orientationLabel(desiredExposure),
			input.Decision.AdjustmentPct*100,
		),
	)

	currentExposure := input.Account.CurrentExposurePercent

	if math.Abs(desiredExposure) < exposureEpsilon {
		if math.Abs(currentExposure) < exposureEpsilon {
			result.Notes = append(result.Notes, "目标为空仓，当前已无持仓，无需执行。")
			return result, nil
		}
		result.Status = StatusProceed
		result.TargetExposurePercent = 0
		result.RiskAmount = 0
		result.RecommendedStopLoss = 0
		result.Notes = append(result.Notes, "目标仓位为 0，执行全仓平仓。")
		return result, nil
	}

	maxExp := m.cfg.MaxExposure
	if maxExp <= 0 {
		maxExp = 0.20
	}
	if math.Abs(desiredExposure) > maxExp {
		result.Notes = append(result.Notes,
			fmt.Sprintf("AI 目标仓位 %.2f%% 超过最大限制 %.2f%%，按上限执行。",
				math.Abs(desiredExposure)*100, maxExp*100,
			),
		)
		desiredExposure = math.Copysign(maxExp, desiredExposure)
	}

	if input.Account.Equity <= 0 {
		result.Notes = append(result.Notes, "账户净值无效，无法评估仓位。")
		return result, nil
	}

	if input.MarketPrice <= 0 {
		result.Notes = append(result.Notes, "缺少有效的市价，无法计算仓位。")
		return result, nil
	}

	if status.Halted {
		increase := math.Abs(desiredExposure) > math.Abs(currentExposure)+exposureEpsilon || desiredExposure*currentExposure < 0
		if increase {
			result.Notes = append(result.Notes, "当日累计亏损已达到限制，拒绝建立新仓位。")
			return result, nil
		}
	}

	confidenceFactor, applied := m.confidenceFactor(input.Decision.Confidence)
	result.ConfidenceApplied = applied

	targetDirection := orientationLabel(desiredExposure)
	finalStop := selectStop(targetDirection, stopLoss, input.Features.Volatility.ATRAbsolute, input.MarketPrice)
	if math.Abs(desiredExposure) > exposureEpsilon && finalStop <= 0 {
		result.Notes = append(result.Notes, "缺少有效止损，无法控制风险。")
		return result, nil
	}
	if math.Abs(desiredExposure) > exposureEpsilon {
		stopDistance := computeStopDistance(targetDirection, input.MarketPrice, finalStop)
		if stopDistance <= 0 {
			result.Notes = append(result.Notes, "止损位置不合理，无法计算风险敞口。")
			return result, nil
		}
		result.RecommendedStopLoss = finalStop
	}

	increase := math.Abs(desiredExposure) > math.Abs(currentExposure)+exposureEpsilon || desiredExposure*currentExposure < 0

	if increase && confidenceFactor <= 0 {
		result.Notes = append(result.Notes, "模型信心度不足，拒绝加仓。")
		return result, nil
	}

	finalExposure := desiredExposure

	if increase {
		stopDistance := computeStopDistance(targetDirection, input.MarketPrice, result.RecommendedStopLoss)
		riskPerTrade := m.cfg.MaxTradeRisk * confidenceFactor
		riskAmount := input.Account.Equity * riskPerTrade
		if riskAmount <= 0 {
			result.Notes = append(result.Notes, "风险额度为零，禁止开仓。")
			return result, nil
		}

		targetByRisk := riskPerTrade * (input.MarketPrice / stopDistance)
		if math.IsNaN(targetByRisk) || math.IsInf(targetByRisk, 0) || targetByRisk <= 0 {
			result.Notes = append(result.Notes, "风险测算失败，无法确定目标仓位。")
			return result, nil
		}

		if targetByRisk > maxExp {
			targetByRisk = maxExp
		}

		finalAbs := math.Min(math.Abs(desiredExposure), targetByRisk)
		if finalAbs <= exposureEpsilon {
			result.Notes = append(result.Notes, "风险限制后无法建立有效仓位。")
			return result, nil
		}

		finalExposure = math.Copysign(finalAbs, desiredExposure)
		if math.Abs(finalExposure-currentExposure) < exposureEpsilon {
			result.Notes = append(result.Notes, "风险限制后目标仓位与当前几乎一致，无需执行。")
			return result, nil
		}

		result.RiskAmount = riskAmount
		result.Notes = append(result.Notes,
			fmt.Sprintf("AI 目标仓位 %.2f%%，风险限制后执行 %.2f%%。",
				desiredExposure*100, finalExposure*100,
			),
		)
	} else {
		if math.Abs(finalExposure-currentExposure) < exposureEpsilon {
			result.Notes = append(result.Notes, "目标仓位与当前仓位差异不足，无需执行。")
			return result, nil
		}
		result.RiskAmount = 0
		result.Notes = append(result.Notes,
			fmt.Sprintf("调整仓位至 %.2f%%，当前仓位为 %.2f%%。",
				finalExposure*100, currentExposure*100,
			),
		)
	}

	result.Status = StatusProceed
	result.TargetExposurePercent = finalExposure
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

func deriveDesiredExposure(intent, direction string, target, adjustment, current float64, currentSide string) float64 {
	dir := strings.ToUpper(direction)
	inten := strings.ToUpper(intent)

	if inten == "CLOSE" || dir == "FLAT" {
		return 0
	}

	sign := resolveExposureSign(dir, current, currentSide, inten)
	desired := current

	if target > 0 {
		desired = target * sign
	} else if math.Abs(current) < exposureEpsilon {
		desired = target * sign
	}

	if adjustment != 0 {
		desired = current + adjustment*sign
	}

	return desired
}

func resolveExposureSign(direction string, current float64, currentSide string, intent string) float64 {
	switch direction {
	case "LONG":
		return 1
	case "SHORT":
		return -1
	case "FLAT":
		return 0
	}

	if strings.ToUpper(currentSide) == "LONG" {
		return 1
	}
	if strings.ToUpper(currentSide) == "SHORT" {
		return -1
	}

	if intent == "HEDGE" && math.Abs(current) > exposureEpsilon {
		return -math.Copysign(1, current)
	}

	if current > 0 {
		return 1
	}
	if current < 0 {
		return -1
	}
	return 1
}

func orientationLabel(value float64) string {
	switch {
	case value > exposureEpsilon:
		return "LONG"
	case value < -exposureEpsilon:
		return "SHORT"
	default:
		return "FLAT"
	}
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
