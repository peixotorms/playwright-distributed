package redis

import (
	"context"
	"fmt"
	"proxy/pkg/config"
	"proxy/pkg/logger"
	"time"

	"github.com/redis/go-redis/v9"
)

type Client struct {
	rd  *redis.Client
	cfg *config.Config
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

	return &Client{
		rd:  rd,
		cfg: cfg,
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

func (c *Client) GetAvailableServers(ctx context.Context) ([]ServerInfo, error) {
	var cursor uint64
	var err error
	var keys []string

	for {
		var batch []string
		batch, cursor, err = c.rd.Scan(ctx, cursor, "worker:*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("error while getting available servers: %w", err)
		}
		keys = append(keys, batch...)
		if cursor == 0 {
			break
		}
	}

	servers := make([]ServerInfo, 0, len(keys))

	for _, key := range keys {
		status, err := c.rd.HGet(ctx, key, "status").Result()
		if err != nil {
			continue
		}

		if status != "available" {
			continue
		}

		var server ServerInfo
		if err := c.rd.HGetAll(ctx, key).Scan(&server); err != nil {
			logger.WithField("key", key).Errorf("Failed to get or scan server info: %v", err)
			continue
		}

		servers = append(servers, server)
	}

	logger.Info(fmt.Sprintf("Available %d/%d registered servers", len(servers), len(keys)))

	return servers, nil
}
