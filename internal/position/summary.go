package position

type Summary struct {
	Side                 string  `json:"side"`
	SizePercent          float64 `json:"size_percent"`
	EntryPrice           float64 `json:"entry_price"`
	UnrealizedPnlPercent float64 `json:"unrealized_pnl_percent"`
	PositionAgeHours     float64 `json:"position_age_hours"`
	StopLoss             float64 `json:"stop_loss"`
	TakeProfit           float64 `json:"take_profit"`
}

func EmptySummary() Summary {
	return Summary{}
}
