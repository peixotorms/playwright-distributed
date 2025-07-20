package redis

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"proxy/pkg/config"
	"proxy/pkg/logger"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	activeConnectionsKey   = "cluster:active_connections"
	lifetimeConnectionsKey = "cluster:lifetime_connections"
)

//go:embed selector.lua
var selectorScriptSource string

var ErrNoAvailableWorkers = errors.New("no available workers")

type Client struct {
	rd             *redis.Client
	cfg            *config.Config
	selectorScript *redis.Script
}

func NewClient(cfg *config.Config) (*Client, error) {
	rd := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%d", cfg.RedisHost, cfg.RedisPort),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rd.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	logger.Info("Connected to Redis")

	selector := redis.NewScript(selectorScriptSource)

	return &Client{
		rd:             rd,
		cfg:            cfg,
		selectorScript: selector,
	}, nil
}

func (c *Client) Close() error {
	return c.rd.Close()
}

type ServerInfo struct {
	ID            string `redis:"id"`
	Endpoint      string `redis:"endpoint"`
	Status        string `redis:"status"`
	StartedAt     string `redis:"startedAt"`
	LastHeartbeat string `redis:"lastHeartbeat"`
}

func (c *Client) GetAllActiveConnections(ctx context.Context) (map[string]int64, error) {
	result := make(map[string]int64)

	vals, err := c.rd.HGetAll(ctx, activeConnectionsKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get all active connections: %w", err)
	}

	for workerId, val := range vals {
		count, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			logger.WithField("workerId", workerId).Errorf("Failed to parse active connections count: %v", err)
			continue
		}
		result[workerId] = count
	}

	return result, nil
}

func (c *Client) GetWorkerActiveConnections(ctx context.Context, workerId string) (int64, error) {
	val, err := c.rd.HGet(ctx, activeConnectionsKey, workerId).Int64()
	if err == redis.Nil {
		return 0, nil
	} else if err != nil {
		return 0, fmt.Errorf("failed to get active connections for worker %s: %w", workerId, err)
	}

	return val, nil
}

func (c *Client) GetLifetimeConnections(ctx context.Context, workerId string) (int64, error) {
	val, err := c.rd.HGet(ctx, lifetimeConnectionsKey, workerId).Int64()
	if err == redis.Nil {
		return 0, nil
	} else if err != nil {
		return 0, fmt.Errorf("failed to get lifetime connections for worker %s: %w", workerId, err)
	}

	return val, nil
}

func (c *Client) ModifyActiveConnections(ctx context.Context, workerId string, delta int64) error {
	err := c.rd.HIncrBy(ctx, activeConnectionsKey, workerId, int64(delta)).Err()
	if err != nil {
		return fmt.Errorf("failed to modify active connections for worker %s: %w", workerId, err)
	}

	return nil
}

func (c *Client) ModifyLifetimeConnections(ctx context.Context, workerId string, delta int64) error {
	err := c.rd.HIncrBy(ctx, lifetimeConnectionsKey, workerId, int64(delta)).Err()
	if err != nil {
		return fmt.Errorf("failed to modify lifetime connections for worker %s: %w", workerId, err)
	}

	return nil
}

func (c *Client) GetWorker(ctx context.Context, workerId string) (ServerInfo, error) {
	workerKey := fmt.Sprintf("worker:%s", workerId)
	var serverInfo ServerInfo

	if err := c.rd.HGetAll(ctx, workerKey).Scan(&serverInfo); err != nil {
		return ServerInfo{}, fmt.Errorf("failed to get worker %s: %w", workerId, err)
	}

	if serverInfo.ID == "" {
		return ServerInfo{}, fmt.Errorf("worker %s not found", workerId)
	}

	return serverInfo, nil
}

func (c *Client) SelectWorker(ctx context.Context) (ServerInfo, error) {
	result, err := c.selectorScript.Run(ctx, c.rd, []string{}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return ServerInfo{}, ErrNoAvailableWorkers
		}
		return ServerInfo{}, fmt.Errorf("failed to run selector script: %w", err)
	}

	workerId, ok := result.(string)
	if !ok {
		return ServerInfo{}, fmt.Errorf("selector script returned unexpected type: %T", result)
	}

	return c.GetWorker(ctx, workerId)
}
