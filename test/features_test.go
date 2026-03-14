package test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	"polar/internal/mcp"
	"polar/internal/providers"
	"polar/internal/storage"
	"polar/pkg/contracts"
)

type stubForecastClient struct {
	snapshot contracts.ForecastSnapshot
	err      error
}

func (s stubForecastClient) Fetch(context.Context, string, float64, float64, string) (contracts.ForecastSnapshot, error) {
	if s.err != nil {
		return contracts.ForecastSnapshot{}, s.err
	}
	return s.snapshot, nil
}

func newTestStack(t *testing.T, cfg config.Config, forecastClient providers.ForecastClient) (*storage.Repository, *core.Service, *api.Server, *mcp.Server) {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	repo := storage.NewRepository(db)
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	svc := core.NewService(cfg, repo, collector.NewSimulatorService(cfg), forecastClient)
	authz := auth.NewFromConfig(cfg.Auth)
	authz.SetFailureHook(func(_ int) {
		svc.RecordAuthFailure()
	})

	return repo, svc, api.NewServer(cfg, svc, authz), mcp.NewServer(cfg, svc, authz)
}

func TestForecastEndpointRequiresForecastScope(t *testing.T) {
	cfg := baseConfig()
	cfg.Features.EnableForecast = true
	cfg.Auth = config.AuthConfig{
		Tokens: []config.TokenConfig{
			{Name: "telemetry", Value: "telemetry-token", Scopes: []string{auth.ScopeReadTelemetry}},
		},
	}

	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})

	req := httptest.NewRequest(http.MethodGet, "/v1/forecast/latest", nil)
	req.Header.Set("Authorization", "Bearer telemetry-token")
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestMetricsEndpointReturnsStructuredSnapshot(t *testing.T) {
	cfg := baseConfig()
	cfg.Features.EnableForecast = true
	cfg.Auth = config.AuthConfig{
		Tokens: []config.TokenConfig{
			{Name: "telemetry", Value: "telemetry-token", Scopes: []string{auth.ScopeReadTelemetry}},
			{Name: "admin", Value: "admin-token", Scopes: []string{auth.ScopeAdminConfig}},
		},
	}

	_, svc, server, _ := newTestStack(t, cfg, stubForecastClient{
		snapshot: contracts.ForecastSnapshot{
			StationID: cfg.Station.ID,
			Provider:  "open-meteo",
			Latitude:  cfg.Station.Latitude,
			Longitude: cfg.Station.Longitude,
			FetchedAt: time.Now().UTC(),
			Points:    []contracts.ForecastPoint{{Time: time.Now().UTC()}},
		},
	})

	if err := svc.PullSensorReadings(context.Background()); err != nil {
		t.Fatal(err)
	}

	telemetryReq := httptest.NewRequest(http.MethodGet, "/v1/forecast/latest", nil)
	telemetryReq.Header.Set("Authorization", "Bearer telemetry-token")
	telemetryResp := httptest.NewRecorder()
	server.Handler().ServeHTTP(telemetryResp, telemetryReq)

	adminReq := httptest.NewRequest(http.MethodGet, "/v1/metrics", nil)
	adminReq.Header.Set("Authorization", "Bearer admin-token")
	adminResp := httptest.NewRecorder()
	server.Handler().ServeHTTP(adminResp, adminReq)

	if adminResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", adminResp.Code)
	}

	var payload struct {
		Collector struct {
			RunsTotal int64 `json:"runs_total"`
		} `json:"collector"`
		Auth struct {
			FailuresTotal int64 `json:"failures_total"`
		} `json:"auth"`
		Requests []struct {
			Name          string `json:"name"`
			RequestsTotal int64  `json:"requests_total"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(adminResp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("metrics json decode failed: %v", err)
	}

	if payload.Collector.RunsTotal == 0 {
		t.Fatalf("expected collector runs to be recorded")
	}
	if payload.Auth.FailuresTotal != 1 {
		t.Fatalf("expected 1 auth failure, got %d", payload.Auth.FailuresTotal)
	}
}

func TestMCPToolRequiresAuditScope(t *testing.T) {
	cfg := baseConfig()
	cfg.Auth = config.AuthConfig{
		Tokens: []config.TokenConfig{
			{Name: "telemetry", Value: "telemetry-token", Scopes: []string{auth.ScopeReadTelemetry}},
		},
	}

	_, _, _, mcpServer := newTestStack(t, cfg, stubForecastClient{})

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"get_audit_events","params":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", body)
	req.Header.Set("Authorization", "Bearer telemetry-token")
	rr := httptest.NewRecorder()
	mcpServer.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var payload struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response json decode failed: %v", err)
	}
	if payload.Error.Code != -32003 {
		t.Fatalf("expected auth error code -32003, got %d", payload.Error.Code)
	}
}

func TestLatestForecastMarksExpiredSnapshotsStale(t *testing.T) {
	cfg := baseConfig()
	cfg.Features.EnableForecast = true
	cfg.Polling.ForecastInterval = time.Minute

	repo, svc, _, _ := newTestStack(t, cfg, stubForecastClient{})
	fetchedAt := time.Now().UTC().Add(-3 * time.Minute)
	if err := repo.InsertForecast(context.Background(), contracts.ForecastSnapshot{
		StationID: cfg.Station.ID,
		Provider:  "open-meteo",
		Latitude:  cfg.Station.Latitude,
		Longitude: cfg.Station.Longitude,
		FetchedAt: fetchedAt,
		Points:    []contracts.ForecastPoint{{Time: fetchedAt.Add(time.Hour)}},
	}); err != nil {
		t.Fatal(err)
	}

	forecast, err := svc.LatestForecast(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !forecast.Stale {
		t.Fatalf("expected forecast to be stale")
	}
	if forecast.StaleReason != "expired" {
		t.Fatalf("expected stale reason expired, got %q", forecast.StaleReason)
	}
	if !forecast.FreshUntil.Equal(fetchedAt.Add(2 * cfg.Polling.ForecastInterval)) {
		t.Fatalf("expected fresh_until to be based on forecast interval")
	}
}

func TestLatestForecastMarksRecentFailuresStale(t *testing.T) {
	cfg := baseConfig()
	cfg.Features.EnableForecast = true

	repo, svc, _, _ := newTestStack(t, cfg, stubForecastClient{err: errors.New("provider down")})
	fetchedAt := time.Now().UTC()
	if err := repo.InsertForecast(context.Background(), contracts.ForecastSnapshot{
		StationID: cfg.Station.ID,
		Provider:  "open-meteo",
		Latitude:  cfg.Station.Latitude,
		Longitude: cfg.Station.Longitude,
		FetchedAt: fetchedAt,
		Points:    []contracts.ForecastPoint{{Time: fetchedAt.Add(time.Hour)}},
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.PullForecast(context.Background()); err == nil {
		t.Fatalf("expected forecast pull to fail")
	}

	forecast, err := svc.LatestForecast(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !forecast.Stale {
		t.Fatalf("expected forecast to be stale after provider error")
	}
	if forecast.StaleReason != "provider_error" {
		t.Fatalf("expected stale reason provider_error, got %q", forecast.StaleReason)
	}
	if forecast.LastError != "provider down" {
		t.Fatalf("expected last_error to be populated, got %q", forecast.LastError)
	}
}

func TestReadyzStaysReadyWhenForecastHasNotSucceededYet(t *testing.T) {
	cfg := baseConfig()
	cfg.Features.EnableForecast = true

	_, svc, server, _ := newTestStack(t, cfg, stubForecastClient{})
	if err := svc.PullSensorReadings(context.Background()); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}
