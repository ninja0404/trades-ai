package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"trades-ai/internal/monitor"
)

func startMonitorServer(ctx context.Context, svc *monitor.Service, port int, logger *zap.Logger) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit := 200
		if qs := q.Get("limit"); qs != "" {
			if v, err := strconv.Atoi(qs); err == nil && v > 0 {
				if v > 1000 {
					v = 1000
				}
				limit = v
			}
		}

		eventType := monitor.EventType("")
		if typ := strings.TrimSpace(q.Get("type")); typ != "" {
			eventType = monitor.EventType(strings.ToLower(typ))
		}

		events, err := svc.ListEvents(r.Context(), eventType, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(events); err != nil {
			logger.Warn("写入监控响应失败", zap.Error(err))
		}
	})

	addr := fmt.Sprintf(":%d", port)
	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil && err != http.ErrServerClosed {
			logger.Warn("关闭监控服务失败", zap.Error(err))
		}
	}()

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("监控服务异常", zap.Error(err))
		}
	}()

	logger.Info("监控接口已启动", zap.String("addr", addr))
	return nil
}
