package main

import (
	"context"
	"fmt"
	"os"
	"proxy/internal/proxy"
	"proxy/internal/redis"
	"proxy/pkg/config"
	"proxy/pkg/logger"
	"time"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Printf("error loading config: %v\n", err)
		os.Exit(1)
	}

	logger.Init(cfg)

	rd, err := redis.NewClient(cfg)
	if err != nil {
		fmt.Printf("Error creating redis client: %v\n", err)
		os.Exit(1)
	}
	defer rd.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = rd.GetAvailableServers(ctx)
	if err != nil {
		fmt.Printf("error: %v", err)
		os.Exit(1)
	}

	proxy.StartProxyServer()
}
