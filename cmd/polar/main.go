package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"polar/internal/api"
	"polar/internal/auth"
	"polar/internal/collector"
	"polar/internal/config"
	"polar/internal/core"
	"polar/internal/mcp"
	"polar/internal/providers"
	"polar/internal/storage"
)

func main() {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}

	db, dialect, err := storage.Open(cfg.Storage)
	if err != nil {
		log.Fatalf("database open failed: %v", err)
	}
	defer db.Close()

	repo := storage.NewRepository(db, dialect)
	if err := repo.Migrate(context.Background()); err != nil {
		log.Fatalf("migrate failed: %v", err)
	}

	var collectorSvc collector.Service
	if cfg.Features.EnableAirthings {
		log.Printf("collector: airthings (client_id=%s)", cfg.Airthings.ClientID)
		collectorSvc = collector.NewAirthingsService(cfg, &http.Client{Timeout: 20 * time.Second})
	} else {
		log.Printf("collector: simulator")
		collectorSvc = collector.NewSimulatorService(cfg)
	}

	httpClient := &http.Client{Timeout: 20 * time.Second}
	weatherClient := providers.NewNOAAClient(cfg.Provider.NOAABaseURL, cfg.Provider.NOAAUserAgent, httpClient)
	fallbackClient := providers.NewOpenMeteoClient(httpClient)
	airClient := providers.NewAirNowClient(cfg.Provider.AirNowURL, cfg.Provider.AirNowToken, httpClient)
	svc := core.NewService(cfg, repo, collectorSvc, weatherClient, fallbackClient, airClient)

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	go svc.RunSchedulers(runCtx)

	authz := auth.NewFromConfig(cfg.Auth)
	authz.SetFailureHook(func(_ int) {
		svc.RecordAuthFailure()
	})
	apiServer := api.NewServer(cfg, svc, authz)
	mcpServer := mcp.NewServer(cfg, svc, authz)

	var apiHandler http.Handler = apiServer.Handler()
	var mcpHTTP *http.Server

	errCh := make(chan error, 2)

	if cfg.Features.EnableMCP {
		if cfg.Server.MCPListenAddr == cfg.Server.ListenAddr {
			// Single-port mode: mount /mcp on the same mux as the REST server.
			combined := http.NewServeMux()
			combined.Handle("/mcp", mcpServer.Handler())
			combined.Handle("/", apiServer.Handler())
			apiHandler = combined
			log.Printf("MCP mounted at %s/mcp (single-port mode)", cfg.Server.ListenAddr)
		} else {
			mcpHTTP = &http.Server{Addr: cfg.Server.MCPListenAddr, Handler: mcpServer.Handler()}
			go func() {
				log.Printf("MCP listening on %s", cfg.Server.MCPListenAddr)
				errCh <- mcpHTTP.ListenAndServe()
			}()
		}
	}

	apiHTTP := &http.Server{Addr: cfg.Server.ListenAddr, Handler: apiHandler}
	go func() {
		log.Printf("REST listening on %s", cfg.Server.ListenAddr)
		errCh <- apiHTTP.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("signal received: %s", sig)
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server failed: %v", err)
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
