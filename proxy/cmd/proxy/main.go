package main

import (
	"fmt"
	"os"
	"proxy/internal/proxy"
	"proxy/internal/redis"
	"proxy/pkg/config"
	"proxy/pkg/logger"
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

	proxy.StartProxyServer(cfg, rd)
}
