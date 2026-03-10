package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"polar/internal/api"
	"polar/internal/auth"
	"polar/internal/collector"
	"polar/internal/config"
	"polar/internal/core"
	"polar/internal/mcp"
	"polar/internal/obs"
	"polar/internal/providers"
	"polar/internal/storage"
)

func main() {
	logger := obs.Default

	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		logger.Error("config load failed", "err", err)
		os.Exit(1)
	}

	db, err := sql.Open("sqlite", cfg.Storage.SQLitePath)
	if err != nil {
		logger.Error("sqlite open failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	repo := storage.NewRepository(db)
	if err := repo.Migrate(context.Background()); err != nil {
		logger.Error("migrate failed", "err", err)
		os.Exit(1)
	}

	collectorSvc := collector.NewSimulatorService(cfg)
	forecastClient := providers.NewOpenMeteoClient(http.DefaultClient)
	svc := core.NewService(cfg, repo, collectorSvc, forecastClient)

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	go svc.RunSchedulers(runCtx)

	authz := auth.New(cfg.Auth.ServiceToken)
	apiServer := api.NewServer(cfg, svc, authz)
	mcpServer := mcp.NewServer(cfg, svc, authz)

	apiHTTP := &http.Server{Addr: cfg.Server.ListenAddr, Handler: apiServer.Handler()}

	errCh := make(chan error, 2)
	go func() {
		logger.Info("REST listening", "addr", cfg.Server.ListenAddr)
		errCh <- apiHTTP.ListenAndServe()
	}()

	var mcpHTTP *http.Server
	if cfg.Features.EnableMCP {
		mcpHTTP = &http.Server{Addr: cfg.Server.MCPListenAddr, Handler: mcpServer.Handler()}
		go func() {
			logger.Info("MCP listening", "addr", cfg.Server.MCPListenAddr)
			errCh <- mcpHTTP.ListenAndServe()
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("signal received, shutting down", "signal", sig.String())
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runCancel()
	_ = apiHTTP.Shutdown(ctx)
	if mcpHTTP != nil {
		_ = mcpHTTP.Shutdown(ctx)
	}
}
