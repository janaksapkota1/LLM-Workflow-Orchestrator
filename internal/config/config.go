package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	// Server
	Port string

	// PostgreSQL
	DatabaseURL string

	// Redis
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// LLM
	AnthropicAPIKey string
	LLMModel        string
	LLMMaxTokens    int
	// LLMMode: "mock" skips real API calls; anything else uses Anthropic.
	LLMMode string

	// Worker
	WorkerConcurrency int
	MaxRetries        int
}

// Load reads configuration from environment variables.
// It returns an error if any required variable is missing.
func Load() (*Config, error) {
	cfg := &Config{
		Port:              getEnv("PORT", "8080"),
		DatabaseURL:       getEnv("DATABASE_URL", ""),
		RedisAddr:         getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:     getEnv("REDIS_PASSWORD", ""),
		LLMModel:          getEnv("LLM_MODEL", "claude-sonnet-4-20250514"),
		AnthropicAPIKey:   getEnv("ANTHROPIC_API_KEY", ""),
		LLMMaxTokens:      getEnvInt("LLM_MAX_TOKENS", 4096),
		LLMMode:           getEnv("LLM_MODE", ""),
		WorkerConcurrency: getEnvInt("WORKER_CONCURRENCY", 5),
		MaxRetries:        getEnvInt("MAX_RETRIES", 3),
	}

	cfg.RedisDB = getEnvInt("REDIS_DB", 0)

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	// API key is only required when not using mock mode.
	if cfg.LLMMode != "mock" && cfg.AnthropicAPIKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is required (or set LLM_MODE=mock to test without a key)")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok {
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return fallback
}