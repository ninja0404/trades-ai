package risk

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"trades-ai/internal/config"
)

// DailyTracker 维护日度风控状态。
type DailyTracker struct {
	db     *sql.DB
	cfg    config.RiskConfig
	logger *zap.Logger
}

// NewDailyTracker 创建日度监控器并初始化表结构。
func NewDailyTracker(db *sql.DB, cfg config.RiskConfig, logger *zap.Logger) (*DailyTracker, error) {
	if db == nil {
		return nil, errors.New("risk: 数据库实例不能为空")
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	tracker := &DailyTracker{
		db:     db,
		cfg:    cfg,
		logger: logger,
	}

	if err := tracker.initSchema(); err != nil {
		return nil, err
	}

	return tracker, nil
}

func (t *DailyTracker) initSchema() error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS risk_daily_metrics (
			trading_date TEXT PRIMARY KEY,
			start_equity REAL NOT NULL,
			current_equity REAL NOT NULL,
			halted INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS risk_activity_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			occurred_at TEXT NOT NULL,
			event_type TEXT NOT NULL,
			message TEXT NOT NULL,
			details TEXT,
			trading_date TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_risk_activity_date ON risk_activity_log(trading_date);`,
	}

	for _, stmt := range schema {
		if _, err := t.db.Exec(stmt); err != nil {
			return fmt.Errorf("risk: 初始化表结构失败: %w", err)
		}
	}

	return nil
}

// Update 根据当前净值更新当日状态，返回最新状态。
func (t *DailyTracker) Update(ctx context.Context, ts time.Time, equity float64) (DailyStatus, error) {
	var result DailyStatus

	tradingDate := tradingDay(ts, t.cfg.DailyLossResetHour)
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("risk: 开启事务失败: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var (
		startEquity float64
		haltedInt   int
	)

	row := tx.QueryRowContext(ctx, `SELECT start_equity, halted FROM risk_daily_metrics WHERE trading_date = ?`, tradingDate)
	switch scanErr := row.Scan(&startEquity, &haltedInt); {
	case scanErr == nil:
		if _, execErr := tx.ExecContext(ctx,
			`UPDATE risk_daily_metrics SET current_equity = ?, updated_at = ? WHERE trading_date = ?`,
			equity, now, tradingDate,
		); execErr != nil {
			err = fmt.Errorf("risk: 更新日度净值失败: %w", execErr)
			return result, err
		}
	case errors.Is(scanErr, sql.ErrNoRows):
		if _, execErr := tx.ExecContext(ctx,
			`INSERT INTO risk_daily_metrics (trading_date, start_equity, current_equity, halted, updated_at)
			 VALUES (?, ?, ?, 0, ?)`,
			tradingDate, equity, equity, now,
		); execErr != nil {
			err = fmt.Errorf("risk: 初始化日度净值失败: %w", execErr)
			return result, err
		}

		result = DailyStatus{
			TradingDate:   tradingDate,
			StartEquity:   equity,
			CurrentEquity: equity,
			LossPercent:   0,
			Halted:        false,
		}

		if commitErr := tx.Commit(); commitErr != nil {
			return result, fmt.Errorf("risk: 提交事务失败: %w", commitErr)
		}

		return result, nil
	default:
		err = fmt.Errorf("risk: 查询日度净值失败: %w", scanErr)
		return result, err
	}

	lossPercent := 0.0
	if startEquity > 0 {
		lossPercent = (equity - startEquity) / startEquity
	}
	halted := haltedInt == 1

	if !halted && startEquity > 0 && lossPercent <= -t.cfg.MaxDailyLoss {
		halted = true
		if _, execErr := tx.ExecContext(ctx,
			`UPDATE risk_daily_metrics SET halted = 1, updated_at = ? WHERE trading_date = ?`,
			now, tradingDate,
		); execErr != nil {
			err = fmt.Errorf("risk: 更新日停交易状态失败: %w", execErr)
			return result, err
		}

		msg := fmt.Sprintf("当日累计亏损%.2f%% 超过上限 %.2f%%，触发停交易", lossPercent*100, t.cfg.MaxDailyLoss*100)
		if logErr := t.logEventTx(ctx, tx, tradingDate, "daily_halt", msg, ""); logErr != nil {
			err = logErr
			return result, err
		}

		t.logger.Warn("触发日度亏损限制", zap.String("trading_date", tradingDate), zap.Float64("loss_percent", lossPercent))
	}

	result = DailyStatus{
		TradingDate:   tradingDate,
		StartEquity:   startEquity,
		CurrentEquity: equity,
		LossPercent:   lossPercent,
		Halted:        halted,
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return result, fmt.Errorf("risk: 提交事务失败: %w", commitErr)
	}

	return result, nil
}

// LogEvent 记录风控事件。
func (t *DailyTracker) LogEvent(ctx context.Context, eventType, message, details, tradingDate string) error {
	if eventType == "" {
		return errors.New("risk: eventType 不能为空")
	}
	if tradingDate == "" {
		tradingDate = tradingDay(time.Now().UTC(), t.cfg.DailyLossResetHour)
	}

	_, err := t.db.ExecContext(ctx,
		`INSERT INTO risk_activity_log (occurred_at, event_type, message, details, trading_date)
		 VALUES (?, ?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339), eventType, message, details, tradingDate,
	)
	if err != nil {
		return fmt.Errorf("risk: 写入风险事件日志失败: %w", err)
	}

	return nil
}

func (t *DailyTracker) logEventTx(ctx context.Context, tx *sql.Tx, tradingDate, eventType, message, details string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO risk_activity_log (occurred_at, event_type, message, details, trading_date)
		 VALUES (?, ?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339), eventType, message, details, tradingDate,
	)
	if err != nil {
		return fmt.Errorf("risk: 记录风险事件失败: %w", err)
	}
	return nil
}

func tradingDay(ts time.Time, resetHour int) string {
	if resetHour < 0 || resetHour > 23 {
		resetHour = 0
	}
	utc := ts.UTC()
	shifted := utc.Add(-time.Duration(resetHour) * time.Hour)
	day := time.Date(shifted.Year(), shifted.Month(), shifted.Day(), 0, 0, 0, 0, time.UTC)
	return day.Format("2006-01-02")
}
