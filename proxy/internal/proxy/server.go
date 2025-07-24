package proxy

import (
	"context"
	"net/http"
	"proxy/internal/models"
	"proxy/internal/redis"
	"proxy/pkg/config"
	"proxy/pkg/httputils"
	"proxy/pkg/logger"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	activeConnections int64
)

func metricsHandler(w http.ResponseWriter, r *http.Request) {
	count := atomic.LoadInt64(&activeConnections)
	httputils.JSONResponse(w, 200, models.MetricsResponse{
		ActiveConnections: count,
	})
}

func StartProxyServer(cfg *config.Config, rd *redis.Client) {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", metricsHandler)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/", proxyHandler(rd))

	server := &http.Server{
		Addr:         ":8080",
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
		Handler:      mux,
	}

	go func() {
		ticker := time.NewTicker(time.Duration(cfg.ReaperRunInterval) * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			logger.Debug("Running reaper to clean up stale workers...")
			reapedCount, err := rd.ReapStaleWorkers(context.Background())
			if err != nil {
				logger.Error("Reaper error: %v", err)
			}
			if reapedCount > 0 {
				logger.Info("Reaper cleaned up %d stale worker(s)", reapedCount)
			} else {
				logger.Debug("Reaper found no stale workers to clean up.")
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			count := atomic.LoadInt64(&activeConnections)
			logger.Info("Active connections: %d", count)
		}
	}()

	logger.Info("Starting proxy server at :8080")
	server.ListenAndServe()
}
