package config

import (
	"fmt"
	"log"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

type Config struct {
	RedisHost             string `mapstructure:"REDIS_HOST"`
	RedisPort             int    `mapstructure:"REDIS_PORT"`
	MaxConcurrentSessions int    `mapstructure:"MAX_CONCURRENT_SESSIONS"`
	MaxLifetimeSessions   int    `mapstructure:"MAX_LIFETIME_SESSIONS"`
	ReaperRunInterval     int    `mapstructure:"REAPER_RUN_INTERVAL"`
	ShutdownCommandTTL    int    `mapstructure:"SHUTDOWN_COMMAND_TTL"`
	WorkerSelectTimeout   int    `mapstructure:"WORKER_SELECT_TIMEOUT"`
	LogLevel              string `mapstructure:"LOG_LEVEL"`
	LogFormat             string `mapstructure:"LOG_FORMAT"`
}

func LoadConfig() (*Config, error) {
	err := godotenv.Load()
	if err != nil {
		log.Printf("Error loading .env file: %v", err)
	}

	viper.BindEnv("REDIS_HOST")
	viper.BindEnv("REDIS_PORT")
	viper.BindEnv("LOG_LEVEL")
	viper.BindEnv("LOG_FORMAT")
	viper.BindEnv("MAX_CONCURRENT_SESSIONS")
	viper.BindEnv("MAX_LIFETIME_SESSIONS")
	viper.BindEnv("REAPER_RUN_INTERVAL")
	viper.BindEnv("SHUTDOWN_COMMAND_TTL")
	viper.BindEnv("WORKER_SELECT_TIMEOUT")

	viper.SetDefault("MAX_CONCURRENT_SESSIONS", 5)
	viper.SetDefault("MAX_LIFETIME_SESSIONS", 50)
	viper.SetDefault("REAPER_RUN_INTERVAL", 300)
	viper.SetDefault("SHUTDOWN_COMMAND_TTL", 60)
	viper.SetDefault("WORKER_SELECT_TIMEOUT", 5)

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	// TODO: use validator
	if cfg.RedisHost == "" {
		return nil, fmt.Errorf("REDIS_HOST is required")
	}
	if cfg.RedisPort == 0 {
		return nil, fmt.Errorf("REDIS_PORT is required")
	}

	return &cfg, nil
}
