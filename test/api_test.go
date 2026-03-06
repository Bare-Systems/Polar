package test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"polar/internal/api"
	"polar/internal/auth"
	"polar/internal/collector"
	"polar/internal/config"
	"polar/internal/core"
	"polar/internal/providers"
	"polar/internal/storage"
)

func baseConfig() config.Config {
	return config.Config{
		Profile:  "simulator",
		Station:  config.StationConfig{ID: "test", Latitude: 1, Longitude: 1},
		Server:   config.ServerConfig{ListenAddr: ":0", MCPListenAddr: ":0"},
		Storage:  config.StorageConfig{SQLitePath: ":memory:"},
		Auth:     config.AuthConfig{ServiceToken: "dev-token"},
		Features: config.FeatureFlags{EnableForecast: false, EnableMCP: true},
		Polling:  config.PollingConfig{SensorInterval: 1 * time.Second, ForecastInterval: 1 * time.Hour},
		Provider: config.ProviderConfig{OpenMeteoURL: "https://example.com"},
	}
}

func TestHealthEndpoint(t *testing.T) {
	cfg := baseConfig()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	repo := storage.NewRepository(db)
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc := core.NewService(cfg, repo, collector.NewSimulatorService(cfg), providers.NewOpenMeteoClient(http.DefaultClient))
	server := api.NewServer(cfg, svc, auth.New(cfg.Auth.ServiceToken))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestCapabilitiesRequiresAuth(t *testing.T) {
	cfg := baseConfig()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	repo := storage.NewRepository(db)
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc := core.NewService(cfg, repo, collector.NewSimulatorService(cfg), providers.NewOpenMeteoClient(http.DefaultClient))
	server := api.NewServer(cfg, svc, auth.New(cfg.Auth.ServiceToken))

	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}
