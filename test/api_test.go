package test

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"polar/pkg/contracts"
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

func TestReadyzNotReadyBeforeCollectorRun(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response json decode failed: %v", err)
	}
	if payload["status"] != "not_ready" {
		t.Fatalf("expected status not_ready, got %v", payload["status"])
	}
}

func TestReadyzReadyAfterCollectorRun(t *testing.T) {
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
	if err := svc.PullSensorReadings(context.Background()); err != nil {
		t.Fatal(err)
	}
	server := api.NewServer(cfg, svc, auth.New(cfg.Auth.ServiceToken))

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response json decode failed: %v", err)
	}
	if payload["status"] != "ready" {
		t.Fatalf("expected status ready, got %v", payload["status"])
	}
}

func TestQueryReadingsResolutionAggregation(t *testing.T) {
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

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	samples := []contracts.Reading{
		{
			StationID:   cfg.Station.ID,
			SensorID:    "sim-temp",
			Metric:      "temperature",
			Value:       10,
			Unit:        "C",
			Source:      "simulator",
			QualityFlag: contracts.QualityGood,
			RecordedAt:  base.Add(10 * time.Second),
			ReceivedAt:  base.Add(12 * time.Second),
		},
		{
			StationID:   cfg.Station.ID,
			SensorID:    "sim-temp",
			Metric:      "temperature",
			Value:       20,
			Unit:        "C",
			Source:      "simulator",
			QualityFlag: contracts.QualityGood,
			RecordedAt:  base.Add(70 * time.Second),
			ReceivedAt:  base.Add(71 * time.Second),
		},
		{
			StationID:   cfg.Station.ID,
			SensorID:    "sim-temp",
			Metric:      "temperature",
			Value:       30,
			Unit:        "C",
			Source:      "simulator",
			QualityFlag: contracts.QualityGood,
			RecordedAt:  base.Add(5*time.Minute + 5*time.Second),
			ReceivedAt:  base.Add(5*time.Minute + 6*time.Second),
		},
	}
	if err := repo.InsertReadings(context.Background(), samples); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/readings?metric=temperature&from="+base.Format(time.RFC3339)+"&to="+base.Add(10*time.Minute).Format(time.RFC3339)+"&resolution=5m", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Auth.ServiceToken)
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var payload []contracts.Reading
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response json decode failed: %v", err)
	}
	if len(payload) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(payload))
	}
	if payload[0].RecordedAt != base {
		t.Fatalf("expected first bucket at %s, got %s", base, payload[0].RecordedAt)
	}
	if payload[0].Value != 15 {
		t.Fatalf("expected first bucket average 15, got %v", payload[0].Value)
	}
	if payload[1].RecordedAt != base.Add(5*time.Minute) {
		t.Fatalf("expected second bucket at %s, got %s", base.Add(5*time.Minute), payload[1].RecordedAt)
	}
	if payload[1].Value != 30 {
		t.Fatalf("expected second bucket average 30, got %v", payload[1].Value)
	}
}

func TestQueryReadingsInvalidResolution(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/v1/readings?metric=temperature&resolution=0s", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Auth.ServiceToken)
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}
