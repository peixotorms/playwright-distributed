package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"

	"proxy/internal/models"
	"proxy/internal/redis"
	"proxy/pkg/config"
	"proxy/pkg/logger"
)

func init() {
	logger.Log = logrus.New()
}

type fakeRedisClient struct {
	selectWorkerFunc            func(ctx context.Context, browserType string) (redis.ServerInfo, error)
	triggerWorkerShutdownFunc   func(ctx context.Context, serverInfo *redis.ServerInfo)
	modifyActiveConnectionsFunc func(ctx context.Context, serverInfo *redis.ServerInfo, delta int64) error
}

func (f *fakeRedisClient) SelectWorker(ctx context.Context, browserType string) (redis.ServerInfo, error) {
	if f.selectWorkerFunc != nil {
		return f.selectWorkerFunc(ctx, browserType)
	}
	return redis.ServerInfo{}, nil
}

func (f *fakeRedisClient) TriggerWorkerShutdownIfNeeded(ctx context.Context, serverInfo *redis.ServerInfo) {
	if f.triggerWorkerShutdownFunc != nil {
		f.triggerWorkerShutdownFunc(ctx, serverInfo)
	}
}

func (f *fakeRedisClient) ModifyActiveConnections(ctx context.Context, serverInfo *redis.ServerInfo, delta int64) error {
	if f.modifyActiveConnectionsFunc != nil {
		return f.modifyActiveConnectionsFunc(ctx, serverInfo, delta)
	}
	return nil
}

func newTestConfig() *config.Config {
	return &config.Config{
		DefaultBrowserType:  "chromium",
		WorkerSelectTimeout: 0,
	}
}

func TestProxyHandler_InvalidBrowserType(t *testing.T) {
	handler := proxyHandler(&fakeRedisClient{}, newTestConfig())

	req := httptest.NewRequest("GET", "/?browser=opera", nil)
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, resp.Code)
	}

	var body models.ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body.Error.Message == "" {
		t.Fatalf("expected error message, got empty string")
	}
}

func TestProxyHandler_NonWebSocketRequest(t *testing.T) {
	handler := proxyHandler(&fakeRedisClient{}, newTestConfig())

	req := httptest.NewRequest("GET", "/", nil)
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusUpgradeRequired {
		t.Fatalf("expected status %d, got %d", http.StatusUpgradeRequired, resp.Code)
	}

	var body models.MessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body.Message == "" {
		t.Fatalf("expected message, got empty string")
	}
}

func TestProxyHandler_NoAvailableWorkers(t *testing.T) {
	fake := &fakeRedisClient{
		selectWorkerFunc: func(ctx context.Context, browserType string) (redis.ServerInfo, error) {
			return redis.ServerInfo{}, redis.ErrNoAvailableWorkers
		},
	}
	handler := proxyHandler(fake, newTestConfig())

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "test")
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, resp.Code)
	}
}

func TestProxyHandler_SelectWorkerUnexpectedError(t *testing.T) {
	expectedErr := errors.New("boom")
	fake := &fakeRedisClient{
		selectWorkerFunc: func(ctx context.Context, browserType string) (redis.ServerInfo, error) {
			return redis.ServerInfo{}, expectedErr
		},
	}
	handler := proxyHandler(fake, newTestConfig())

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "test")
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, resp.Code)
	}
}

func TestSelectWorkerWithRetryRetriesUntilSuccess(t *testing.T) {
	attempts := 0
	fake := &fakeRedisClient{
		selectWorkerFunc: func(ctx context.Context, browserType string) (redis.ServerInfo, error) {
			attempts++
			if attempts < 2 {
				return redis.ServerInfo{}, redis.ErrNoAvailableWorkers
			}
			return redis.ServerInfo{ID: "worker-1"}, nil
		},
	}

	timeout := time.Second
	server, err := selectWorkerWithRetry(context.Background(), fake, timeout, "chromium")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}

	if server.ID != "worker-1" {
		t.Fatalf("expected worker ID 'worker-1', got %s", server.ID)
	}
}

func TestSelectWorkerWithRetryTimeout(t *testing.T) {
	fake := &fakeRedisClient{
		selectWorkerFunc: func(ctx context.Context, browserType string) (redis.ServerInfo, error) {
			return redis.ServerInfo{}, redis.ErrNoAvailableWorkers
		},
	}

	timeout := 200 * time.Millisecond
	_, err := selectWorkerWithRetry(context.Background(), fake, timeout, "chromium")
	if !errors.Is(err, redis.ErrNoAvailableWorkers) {
		t.Fatalf("expected ErrNoAvailableWorkers, got %v", err)
	}
}

func TestProxyHandler_SuccessfulConnectionLifecycle(t *testing.T) {
	atomic.StoreInt64(&activeConnections, 0)
	t.Cleanup(func() {
		atomic.StoreInt64(&activeConnections, 0)
	})

	backendUpgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := backendUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("backend upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		for {
			msgType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(msgType, payload); err != nil {
				return
			}
		}
	}))
	defer backend.Close()

	workerEndpoint := "ws" + strings.TrimPrefix(backend.URL, "http")

	shutdownCalled := make(chan struct{}, 1)
	modifyCalls := make(chan int64, 1)

	fake := &fakeRedisClient{
		selectWorkerFunc: func(ctx context.Context, browserType string) (redis.ServerInfo, error) {
			return redis.ServerInfo{ID: "worker-1", Endpoint: workerEndpoint}, nil
		},
		triggerWorkerShutdownFunc: func(ctx context.Context, serverInfo *redis.ServerInfo) {
			shutdownCalled <- struct{}{}
		},
		modifyActiveConnectionsFunc: func(ctx context.Context, serverInfo *redis.ServerInfo, delta int64) error {
			modifyCalls <- delta
			return nil
		},
	}

	cfg := newTestConfig()
	cfg.WorkerSelectTimeout = 1

	proxyServer := httptest.NewServer(http.HandlerFunc(proxyHandler(fake, cfg)))
	defer proxyServer.Close()

	proxyURL := "ws" + strings.TrimPrefix(proxyServer.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(proxyURL, nil)
	if err != nil {
		t.Fatalf("failed to dial proxy: %v", err)
	}

	if err := clientConn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("failed to send message through proxy: %v", err)
	}

	if err := clientConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("failed to set read deadline: %v", err)
	}
	msgType, echo, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read echo message: %v", err)
	}
	if msgType != websocket.TextMessage || string(echo) != "ping" {
		t.Fatalf("unexpected echo response: type=%d payload=%q", msgType, string(echo))
	}

	if err := clientConn.Close(); err != nil {
		t.Fatalf("failed to close client connection: %v", err)
	}

	select {
	case <-shutdownCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("expected TriggerWorkerShutdownIfNeeded to be called")
	}

	select {
	case delta := <-modifyCalls:
		if delta != -1 {
			t.Fatalf("expected ModifyActiveConnections delta -1, got %d", delta)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected ModifyActiveConnections to be called")
	}
}

func TestProxyHandler_BackendDialFailure(t *testing.T) {
	shutdownCalled := make(chan struct{}, 1)
	modifyCalls := make(chan int64, 1)

	fake := &fakeRedisClient{
		selectWorkerFunc: func(ctx context.Context, browserType string) (redis.ServerInfo, error) {
			return redis.ServerInfo{ID: "worker-1", Endpoint: "ws://127.0.0.1:1"}, nil
		},
		triggerWorkerShutdownFunc: func(ctx context.Context, serverInfo *redis.ServerInfo) {
			shutdownCalled <- struct{}{}
		},
		modifyActiveConnectionsFunc: func(ctx context.Context, serverInfo *redis.ServerInfo, delta int64) error {
			modifyCalls <- delta
			return nil
		},
	}

	handler := proxyHandler(fake, newTestConfig())

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "test")

	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, resp.Code)
	}

	select {
	case <-shutdownCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("expected TriggerWorkerShutdownIfNeeded to be called")
	}

	select {
	case delta := <-modifyCalls:
		t.Fatalf("ModifyActiveConnections should not be called, but received delta %d", delta)
	default:
	}
}

func TestMetricsHandlerReportsActiveConnections(t *testing.T) {
	atomic.StoreInt64(&activeConnections, 37)
	t.Cleanup(func() {
		atomic.StoreInt64(&activeConnections, 0)
	})

	req := httptest.NewRequest("GET", "/metrics", nil)
	resp := httptest.NewRecorder()

	metricsHandler(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.Code)
	}

	var metrics models.MetricsResponse
	if err := json.NewDecoder(resp.Body).Decode(&metrics); err != nil {
		t.Fatalf("failed to decode metrics response: %v", err)
	}

	if metrics.ActiveConnections != 37 {
		t.Fatalf("expected active connections 37, got %d", metrics.ActiveConnections)
	}
}
