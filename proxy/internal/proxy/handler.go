package proxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"proxy/internal/models"
	"proxy/internal/redis"
	"proxy/pkg/httputils"
	"proxy/pkg/logger"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	maxRetries = 3
	retryDelay = 500 * time.Millisecond
)

func selectWorkerWithRetry(ctx context.Context, rd *redis.Client) (redis.ServerInfo, error) {
	var server redis.ServerInfo
	var err error

	for i := range maxRetries {
		server, err = rd.SelectWorker(ctx)
		if err == nil {
			return server, nil
		}

		if !errors.Is(err, redis.ErrNoAvailableWorkers) {
			return redis.ServerInfo{}, err
		}

		if i == maxRetries-1 {
			break
		}

		logger.Debug("No workers available, retrying attempt %d/%d in %v...", i+1, maxRetries, retryDelay)

		select {
		case <-time.After(retryDelay):
			continue
		case <-ctx.Done():
			return redis.ServerInfo{}, ctx.Err()
		}
	}

	return redis.ServerInfo{}, err
}

func proxyHandler(rd *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		if !websocket.IsWebSocketUpgrade(r) {
			httputils.JSONResponse(w, http.StatusUpgradeRequired, models.MessageResponse{
				Message: "This endpoint is for WebSocket connections only.",
			})
			return
		}

		server, err := selectWorkerWithRetry(r.Context(), rd)
		if err != nil {
			if errors.Is(err, redis.ErrNoAvailableWorkers) {
				logger.Error(
					"Connection from %s rejected. No workers available after %d retries: %v",
					r.RemoteAddr,
					maxRetries,
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
		logger.Info("Proxy connection established (%s <-> %s). Active connections: %d", r.RemoteAddr, server.Endpoint, atomic.LoadInt64(&activeConnections))
		defer func() {
			atomic.AddInt64(&activeConnections, -1)
			// `rd.SelectWorker` is increasing this counter during selection process
			rd.ModifyActiveConnections(r.Context(), server.ID, -1)
			logger.Info("Proxy connection closed (%s <-> %s). Active connections: %d", r.RemoteAddr, server.Endpoint, atomic.LoadInt64(&activeConnections))
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
