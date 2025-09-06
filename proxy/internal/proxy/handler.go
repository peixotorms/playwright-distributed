package proxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"proxy/internal/models"
	"proxy/internal/redis"
	"proxy/pkg/config"
	"proxy/pkg/httputils"
	"proxy/pkg/logger"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	retryDelay = 500 * time.Millisecond
)

func selectWorkerWithRetry(ctx context.Context, rd *redis.Client, timeout time.Duration, browserType string) (redis.ServerInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(retryDelay)
	defer ticker.Stop()

	for {
		server, err := rd.SelectWorker(ctx, browserType)
		if err == nil {
			return server, nil
		}

		if !errors.Is(err, redis.ErrNoAvailableWorkers) {
			return redis.ServerInfo{}, err
		}

		select {
		case <-ticker.C:
			continue
		case <-ctx.Done():
			return redis.ServerInfo{}, redis.ErrNoAvailableWorkers
		}
	}
}

func proxyHandler(rd *redis.Client, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var browserType string
		if r.URL.Path == "/" || r.URL.Path == "/firefox" {
			browserType = "chromium"
			if strings.Contains(r.URL.Path, "firefox") {
				browserType = "firefox"
			}
		} else {
			http.NotFound(w, r)
			return
		}

		if !websocket.IsWebSocketUpgrade(r) {
			httputils.JSONResponse(w, http.StatusUpgradeRequired, models.MessageResponse{
				Message: "This endpoint is for WebSocket connections only.",
			})
			return
		}

		timeout := time.Duration(cfg.WorkerSelectTimeout) * time.Second
		server, err := selectWorkerWithRetry(r.Context(), rd, timeout, browserType)
		if err != nil {
			if errors.Is(err, redis.ErrNoAvailableWorkers) {
				logger.Error(
					"Connection from %s rejected. No workers available after %v timeout: %v",
					r.RemoteAddr,
					timeout,
					err,
				)
				httputils.ErrorResponse(w, http.StatusServiceUnavailable, "No available servers")
			} else {
				logger.Error(
					"Connection from %s rejected. An unexpected error occurred while selecting a worker: %v",
					r.RemoteAddr,
					err,
				)
				httputils.ErrorResponse(w, http.StatusInternalServerError, "An internal error occurred")
			}
			return
		}

		go rd.TriggerWorkerShutdownIfNeeded(r.Context(), &server)

		backendURL, _ := url.Parse(server.Endpoint)
		serverConn, _, err := websocket.DefaultDialer.Dial(backendURL.String(), nil)
		if err != nil {
			logger.Error("Connection from %s rejected. Failed to connect to browser server: %v", r.RemoteAddr, err)
			httputils.ErrorResponse(w, http.StatusInternalServerError, "Browser server error")
			return
		}
		defer serverConn.Close()

		clientConn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logger.Error("Failed to upgrade client connection: %v", err)
			return
		}
		defer clientConn.Close()

		atomic.AddInt64(&activeConnections, 1)
		logger.Info("New connection from %s", r.RemoteAddr)
		logger.Debug("Proxy connection established (%s <-> %s)", r.RemoteAddr, server.Endpoint)
		defer func() {
			atomic.AddInt64(&activeConnections, -1)
			// `rd.SelectWorker` is increasing this counter during selection process
			rd.ModifyActiveConnections(r.Context(), &server, -1)
			logger.Debug("Proxy connection closed (%s <-> %s)", r.RemoteAddr, server.Endpoint)
		}()

		done := make(chan struct{})
		var once sync.Once

		go func() {
			relay(clientConn, serverConn, "client->server")
			once.Do(func() {
				close(done)
			})
		}()

		go func() {
			relay(serverConn, clientConn, "server->client")
			once.Do(func() {
				close(done)
			})
		}()

		<-done
	}
}

func relay(src, dst *websocket.Conn, direction string) {
	srcAddr := src.RemoteAddr()
	dstAddr := dst.RemoteAddr()

	for {
		msgType, message, err := src.ReadMessage()
		if err != nil {
			if e, ok := err.(*websocket.CloseError); ok {
				switch e.Code {
				case websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived:
					logger.Debug("Connection closed normally (%s): %v", direction, err)

				case websocket.CloseAbnormalClosure:
					logger.Debug("Connection closed abnormally (%s): %v", direction, err)

				default:
					logger.Error("Unexpected websocket close error (%s): %v", direction, err)
				}
			} else if errors.Is(err, net.ErrClosed) {
				logger.Debug("Connection closed by proxy teardown (%s)", direction)
			} else {
				logger.Error("Unexpected network error in relay (%s): %v", direction, err)
			}
			return
		}

		err = dst.WriteMessage(msgType, message)
		if err != nil {
			logger.Error("Failed to relay message (%s): %v", direction, err)
			return
		}

		logger.Debug("Relayed %s->%s: %d bytes", srcAddr, dstAddr, len(message))
	}
}
