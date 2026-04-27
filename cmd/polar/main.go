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

	httpClient := &http.Client{Timeout: 20 * time.Second}

	// --- Collectors (B-3: MultiCollector wraps all active collectors) ---

	var collectorServices []collector.Service

	if cfg.Features.EnableAirthings {
		log.Printf("collector: airthings (client_id=%s)", cfg.Airthings.ClientID)
		collectorServices = append(collectorServices, collector.NewAirthingsService(cfg, httpClient))
	}
	if cfg.Features.EnableShelly && len(cfg.Shelly.Devices) > 0 {
		log.Printf("collector: shelly (%d devices)", len(cfg.Shelly.Devices))
		collectorServices = append(collectorServices, collector.NewShellyService(cfg, &http.Client{Timeout: 5 * time.Second}))
	}
	if cfg.Features.EnableSwitchBot && cfg.SwitchBot.Token != "" {
		log.Printf("collector: switchbot (%d devices)", len(cfg.SwitchBot.Devices))
		collectorServices = append(collectorServices, collector.NewSwitchBotService(cfg, httpClient))
	}
	if cfg.Features.EnableNetatmo && cfg.Netatmo.RefreshToken != "" {
		log.Printf("collector: netatmo (client_id=%s)", cfg.Netatmo.ClientID)
		collectorServices = append(collectorServices, collector.NewNetatmoService(cfg, httpClient))
	}

	// Fall back to simulator when no real collector is configured.
	if len(collectorServices) == 0 {
		log.Printf("collector: simulator")
		collectorServices = append(collectorServices, collector.NewSimulatorService(cfg))
	}

	collectorSvc := collector.NewMultiCollector(collectorServices...)

	// --- Weather providers ---

	weatherClient := providers.NewNOAAClient(cfg.Provider.NOAABaseURL, cfg.Provider.NOAAUserAgent, httpClient)
	fallbackClient := providers.NewOpenMeteoClient(httpClient)
	airClient := providers.NewAirNowClient(cfg.Provider.AirNowURL, cfg.Provider.AirNowToken, httpClient)

	// --- Phase C context providers ---

	var opts []core.ServiceOption

	if cfg.Features.EnableAstronomy {
		log.Printf("context: astronomy (in-process, no API key)")
		opts = append(opts, core.WithAstronomy(providers.NewAstronomyProvider()))
	}

	if cfg.Features.EnableWildfire && cfg.Provider.FIRMSAPIKey != "" {
		log.Printf("context: wildfire (NASA FIRMS, radius=%.0f km)", cfg.Provider.FIRMSRadiusKm)
		opts = append(opts, core.WithWildfire(providers.NewFIRMSProvider(cfg.Provider.FIRMSAPIKey, cfg.Provider.FIRMSRadiusKm, httpClient)))
	}

	if (cfg.Features.EnablePollen || cfg.Features.EnableUV) && cfg.Provider.WeatherAPIKey != "" {
		log.Printf("context: pollen/UV (WeatherAPI.com)")
		opts = append(opts, core.WithWeatherAPI(providers.NewWeatherAPIClient(cfg.Provider.WeatherAPIKey, httpClient)))
	}

	if cfg.Features.EnablePurpleAir && cfg.Provider.PurpleAirAPIKey != "" {
		log.Printf("context: neighbourhood AQ (PurpleAir, radius=%.0f km)", cfg.Provider.PurpleAirRadius)
		opts = append(opts, core.WithPurpleAir(providers.NewPurpleAirProvider(cfg.Provider.PurpleAirAPIKey, cfg.Provider.PurpleAirRadius, httpClient)))
	}

	// --- Service ---

	svc := core.NewService(cfg, repo, collectorSvc, weatherClient, fallbackClient, airClient, opts...)

	// Seed provider license metadata on every startup.
	svc.SeedSourceLicenses(context.Background())
	// Seed consent grants reflecting currently configured integrations (X-4).
	svc.SeedConsentGrants(context.Background())

	// --- Schedulers ---

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	go svc.RunSchedulers(runCtx)

	// --- HTTP servers ---

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
