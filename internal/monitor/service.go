package monitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"trades-ai/internal/ai"
	"trades-ai/internal/execution"
	"trades-ai/internal/feature"
	"trades-ai/internal/position"
	"trades-ai/internal/risk"
	"trades-ai/internal/store"
)

// Service 负责持久化监控事件。
type Service struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewService 初始化监控服务，创建所需表结构。
func NewService(store *store.Store, logger *zap.Logger) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("monitor: store 不能为空")
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	s := &Service{
		db:     store.DB(),
		logger: logger,
	}

	if err := s.initSchema(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *Service) initSchema() error {
	stmt := `
CREATE TABLE IF NOT EXISTS monitor_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_type TEXT NOT NULL,
	payload TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_monitor_events_type ON monitor_events(event_type);
`
	if _, err := s.db.Exec(stmt); err != nil {
		return fmt.Errorf("monitor: 初始化表失败: %w", err)
	}
	return nil
}

// Record 写入单个事件。
func (s *Service) Record(ctx context.Context, event Event) error {
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return fmt.Errorf("monitor: 序列化事件失败: %w", err)
	}

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO monitor_events (event_type, payload, created_at) VALUES (?, ?, ?)`,
		string(event.Type), string(payload), event.Timestamp.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("monitor: 写入事件失败: %w", err)
	}

	return nil
}

// RecordMarketSnapshot 记录行情特征。
func (s *Service) RecordMarketSnapshot(ctx context.Context, features feature.FeatureSet) {
	if err := s.Record(ctx, Event{
		Type:      EventMarketSnapshot,
		Timestamp: time.Now().UTC(),
		Payload:   MarketSnapshotPayload{Features: features},
	}); err != nil {
		s.logger.Warn("记录行情事件失败", zap.Error(err))
	}
}

// RecordDecision 记录AI决策。
func (s *Service) RecordDecision(ctx context.Context, features feature.FeatureSet, decision ai.Decision) {
	if err := s.Record(ctx, Event{
		Type:      EventAIDecision,
		Timestamp: time.Now().UTC(),
		Payload:   AIDecisionPayload{Decision: decision, Features: features},
	}); err != nil {
		s.logger.Warn("记录AI决策事件失败", zap.Error(err))
	}
}

// RecordRisk 记录风控评估。
func (s *Service) RecordRisk(ctx context.Context, input risk.EvaluationInput, result risk.EvaluationResult) {
	if err := s.Record(ctx, Event{
		Type:      EventRiskEvaluation,
		Timestamp: time.Now().UTC(),
		Payload:   RiskEvaluationPayload{Input: input, Result: result},
	}); err != nil {
		s.logger.Warn("记录风控事件失败", zap.Error(err))
	}
}

// RecordExecution 记录订单执行。
func (s *Service) RecordExecution(ctx context.Context, plan execution.ExecutionPlan, result execution.Result) {
	if err := s.Record(ctx, Event{
		Type:      EventExecution,
		Timestamp: time.Now().UTC(),
		Payload:   ExecutionPayload{Plan: plan, Result: result},
	}); err != nil {
		s.logger.Warn("记录执行事件失败", zap.Error(err))
	}
}

// RecordPosition 记录账户状态。
func (s *Service) RecordPosition(ctx context.Context, balance position.AccountBalance, details []position.PositionDetail) {
	if err := s.Record(ctx, Event{
		Type:      EventPosition,
		Timestamp: time.Now().UTC(),
		Payload:   PositionPayload{Balance: balance, Positions: details},
	}); err != nil {
		s.logger.Warn("记录仓位事件失败", zap.Error(err))
	}
}

// RecordError 记录异常。
func (s *Service) RecordError(ctx context.Context, msg string, err error, ctxMap map[string]interface{}) {
	payload := ErrorPayload{
		Message: msg,
		Error:   err.Error(),
		Context: ctxMap,
	}
	if recErr := s.Record(ctx, Event{
		Type:      EventError,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}); recErr != nil {
		s.logger.Warn("记录异常事件失败", zap.Error(recErr))
	}
}

// ListEvents 按类型检索最近事件。
func (s *Service) ListEvents(ctx context.Context, eventType EventType, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `SELECT event_type, payload, created_at FROM monitor_events`
	args := make([]interface{}, 0, 2)
	if eventType != "" {
		query += ` WHERE event_type = ?`
		args = append(args, string(eventType))
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("monitor: 查询事件失败: %w", err)
	}
	defer rows.Close()

	events := make([]Event, 0, limit)
	for rows.Next() {
		var (
			typ     string
			payload string
			created string
		)
		if scanErr := rows.Scan(&typ, &payload, &created); scanErr != nil {
			return nil, fmt.Errorf("monitor: 解析事件失败: %w", scanErr)
		}

		ts, parseErr := time.Parse(time.RFC3339, created)
		if parseErr != nil {
			ts = time.Now().UTC()
		}

		events = append(events, Event{
			Type:      EventType(typ),
			Timestamp: ts,
			Payload:   json.RawMessage(payload),
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("monitor: 读取事件失败: %w", err)
	}

	return events, nil
}
