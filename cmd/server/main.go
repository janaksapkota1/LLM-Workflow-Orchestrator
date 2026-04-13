package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"llm-orchestrator/internal/api"
	"llm-orchestrator/internal/config"
	"llm-orchestrator/internal/llm"
	"llm-orchestrator/internal/logger"
	"llm-orchestrator/internal/orchestrator"
	"llm-orchestrator/internal/queue"
	"llm-orchestrator/internal/store"
	"llm-orchestrator/internal/worker"
)

func main() {
	// Load .env if present (ignored in production where real env vars are set).
	_ = godotenv.Load()

	log := logger.New()

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("config load failed")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── Postgres ──────────────────────────────────────────────────────────────
	db, err := store.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("postgres connect failed")
	}
	defer db.Close()
	log.Info().Msg("postgres connected")

	// ── Redis ────────────────────────────────────────────────────────────────
	rdb, err := queue.Connect(ctx, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		log.Fatal().Err(err).Msg("redis connect failed")
	}
	defer rdb.Close()
	log.Info().Msg("redis connected")

	// ── Dependencies ─────────────────────────────────────────────────────────
	s := store.New(db)
	q := queue.New(rdb)
	l := llm.New(cfg.AnthropicAPIKey, cfg.LLMModel, cfg.LLMMaxTokens)

	orch := orchestrator.New(s, q, l, log)

	// ── Worker pool ───────────────────────────────────────────────────────────
	pool := worker.NewPool(cfg.WorkerConcurrency, s, q, l, orch, cfg.MaxRetries, log)
	pool.Start(ctx)
	log.Info().Int("concurrency", cfg.WorkerConcurrency).Msg("worker pool started")

	// ── HTTP server ───────────────────────────────────────────────────────────
	handler := api.New(s, orch, q, log)
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler.Routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info().Str("addr", srv.Addr).Msg("HTTP server listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("HTTP server error")
		}
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutdown signal received")
	cancel() // Stop workers

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("HTTP server shutdown error")
	}

	log.Info().Msg("server exited cleanly")
}