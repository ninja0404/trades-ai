package position

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	ccxt "github.com/ccxt/ccxt/go/v4"
	"go.uber.org/zap"
)

type balanceClient interface {
	FetchBalance(params ...interface{}) (ccxt.Balances, error)
	FetchPositions(options ...ccxt.FetchPositionsOptions) ([]ccxt.Position, error)
}

// AccountBalance 描述账户权益及余额。
type AccountBalance struct {
	TotalEquity     float64
	TotalUSD        float64
	FreeUSD         float64
	Withdrawable    float64
	MarginUsed      float64
	TotalNotional   float64
	CrossEquity     float64
	CrossMarginUsed float64
	Unrealized      float64
	Leverage        float64
	Timestamp       time.Time
}

// PositionDetail 表示单个方向的仓位详情。
type PositionDetail struct {
	Symbol                string
	Side                  string
	Size                  float64
	EntryPrice            float64
	MarkPrice             float64
	LiqPrice              float64
	Notional              float64
	PositionValue         float64
	UnrealizedPn          float64
	ReturnOnEquity        float64
	MarginUsed            float64
	InitialMargin         float64
	Collateral            float64
	Leverage              float64
	MarginMode            string
	CumFundingAllTime     float64
	CumFundingSinceOpen   float64
	CumFundingSinceChange float64
	Timestamp             time.Time
}

// Manager 维护仓位与资金状态。
type Manager struct {
	client balanceClient
	market string
	logger *zap.Logger
}

// NewManager 创建仓位管理器。
func NewManager(client balanceClient, market string, logger *zap.Logger) *Manager {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Manager{
		client: client,
		market: market,
		logger: logger,
	}
}

// FetchSnapshot 获取账户余额与当前仓位。
func (m *Manager) FetchSnapshot(ctx context.Context) (AccountBalance, []PositionDetail, error) {
	var balance AccountBalance
	var positions []PositionDetail

	now := time.Now().UTC()

	balances, err := m.client.FetchBalance()
	if err != nil {
		return balance, positions, fmt.Errorf("position: 获取账户余额失败: %w", err)
	}

	if balances.Total != nil {
		for _, code := range []string{"USDC", "USD", "USDT"} {
			if total, ok := balances.Total[code]; ok && total != nil {
				if balance.TotalUSD == 0 {
					balance.TotalUSD = *total
				}
				if balance.TotalEquity == 0 {
					balance.TotalEquity = *total
				}
			}
		}
		if balance.TotalUSD == 0 {
			for _, v := range balances.Total {
				if v != nil && *v > 0 {
					balance.TotalUSD = *v
					if balance.TotalEquity == 0 {
						balance.TotalEquity = *v
					}
					break
				}
			}
		}
	}
	if balances.Free != nil {
		for _, code := range []string{"USDC", "USD", "USDT"} {
			if free, ok := balances.Free[code]; ok && free != nil {
				balance.FreeUSD = *free
				break
			}
		}
	}
	if balances.Info != nil {
		if summary, ok := balances.Info["marginSummary"].(map[string]interface{}); ok {
			if balance.TotalEquity == 0 {
				if v := parseNumeric(summary["accountValue"]); v > 0 {
					balance.TotalEquity = v
				}
			}
			if balance.TotalUSD == 0 {
				if v := parseNumeric(summary["totalRawUsd"]); v > 0 {
					balance.TotalUSD = v
				}
			}
			if v := parseNumeric(summary["totalMarginUsed"]); v > 0 {
				balance.MarginUsed = v
			}
			if v := parseNumeric(summary["totalNtlPos"]); v > 0 {
				balance.TotalNotional = v
			}
		}
		if cross, ok := balances.Info["crossMarginSummary"].(map[string]interface{}); ok {
			if v := parseNumeric(cross["accountValue"]); v > 0 {
				balance.CrossEquity = v
			}
			if v := parseNumeric(cross["totalMarginUsed"]); v > 0 {
				balance.CrossMarginUsed = v
			}
			if balance.TotalNotional == 0 {
				if v := parseNumeric(cross["totalNtlPos"]); v > 0 {
					balance.TotalNotional = v
				}
			}
		}
		if v := parseNumeric(balances.Info["withdrawable"]); v > 0 {
			balance.Withdrawable = v
			balance.FreeUSD = v
		}
	}

	if balance.TotalEquity == 0 {
		balance.TotalEquity = balance.TotalUSD
	}

	balance.Timestamp = now

	rawPositions, err := m.client.FetchPositions()
	if err != nil {
		return balance, positions, fmt.Errorf("position: 获取持仓失败: %w", err)
	}

	var totalUnrealized float64
	var totalPositionValue float64
	var totalMarginUsed float64

	for _, rawPos := range rawPositions {
		symbol := derefString(rawPos.Symbol)
		if symbol == "" || !strings.EqualFold(symbol, m.market) {
			continue
		}

		size := derefFloat(rawPos.Contracts)
		if size == 0 {
			continue
		}

		side := strings.ToUpper(strings.TrimSpace(derefString(rawPos.Side)))
		if side == "" {
			side = "LONG"
		}

		entry := derefFloat(rawPos.EntryPrice)
		liq := derefFloat(rawPos.LiquidationPrice)
		mark := derefFloat(rawPos.MarkPrice)
		unrealized := derefFloat(rawPos.UnrealizedPnl)
		notional := derefFloat(rawPos.Notional)
		collateral := derefFloat(rawPos.Collateral)
		initialMargin := derefFloat(rawPos.InitialMargin)
		leverage := derefFloat(rawPos.Leverage)
		percentage := derefFloat(rawPos.Percentage)
		marginMode := strings.ToUpper(strings.TrimSpace(derefString(rawPos.MarginMode)))

		positionValue := notional
		marginUsed := collateral
		returnOnEquity := percentage
		cumFundingAll := 0.0
		cumFundingOpen := 0.0
		cumFundingChange := 0.0

		if rawPos.Info != nil {
			if positionInfo, ok := rawPos.Info["position"].(map[string]interface{}); ok {
				if mark == 0 {
					if v := parseNumeric(positionInfo["markPx"]); v > 0 {
						mark = v
					}
				}
				if v := parseNumeric(positionInfo["positionValue"]); v > 0 {
					positionValue = v
				}
				if v := parseNumeric(positionInfo["marginUsed"]); v > 0 {
					marginUsed = v
				}
				if v := parseNumeric(positionInfo["returnOnEquity"]); v != 0 {
					returnOnEquity = v
				}
				if funding, ok := positionInfo["cumFunding"].(map[string]interface{}); ok {
					cumFundingAll = parseNumeric(funding["allTime"])
					cumFundingOpen = parseNumeric(funding["sinceOpen"])
					cumFundingChange = parseNumeric(funding["sinceChange"])
				}
			}
		}

		if marginUsed == 0 {
			marginUsed = collateral
		}
		if positionValue == 0 {
			positionValue = notional
		}
		if returnOnEquity == 0 {
			returnOnEquity = percentage
		}

		totalUnrealized += unrealized
		totalPositionValue += positionValue
		totalMarginUsed += marginUsed

		positions = append(positions, PositionDetail{
			Symbol:                symbol,
			Side:                  side,
			Size:                  size,
			EntryPrice:            entry,
			MarkPrice:             mark,
			LiqPrice:              liq,
			Notional:              notional,
			PositionValue:         positionValue,
			UnrealizedPn:          unrealized,
			ReturnOnEquity:        returnOnEquity,
			MarginUsed:            marginUsed,
			InitialMargin:         initialMargin,
			Collateral:            collateral,
			Leverage:              leverage,
			MarginMode:            marginMode,
			CumFundingAllTime:     cumFundingAll,
			CumFundingSinceOpen:   cumFundingOpen,
			CumFundingSinceChange: cumFundingChange,
			Timestamp:             now,
		})
	}

	balance.Unrealized = totalUnrealized
	if balance.TotalNotional == 0 {
		balance.TotalNotional = totalPositionValue
	}
	if balance.MarginUsed == 0 {
		balance.MarginUsed = totalMarginUsed
	}

	return balance, positions, nil
}

func derefFloat(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func parseNumeric(value interface{}) float64 {
	switch v := value.(type) {
	case nil:
		return 0
	case float64:
		return v
	case *float64:
		if v != nil {
			return *v
		}
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case uint64:
		return float64(v)
	case int32:
		return float64(v)
	case uint32:
		return float64(v)
	case int16:
		return float64(v)
	case uint16:
		return float64(v)
	case int8:
		return float64(v)
	case uint8:
		return float64(v)
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	case fmt.Stringer:
		s := strings.TrimSpace(v.String())
		if s == "" {
			return 0
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f
		}
	case *json.Number:
		if v != nil {
			if f, err := v.Float64(); err == nil {
				return f
			}
		}
	}
	return 0
}
