package test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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

func (s stubForecastClient) Fetch(context.Context, contracts.MonitorTarget, string) (contracts.ForecastSnapshot, error) {
	if s.err != nil {
		return contracts.ForecastSnapshot{}, s.err
	}
	return s.snapshot, nil
}

type stubWeatherClient struct {
	bundle providers.WeatherBundle
	err    error
}

func (s stubWeatherClient) Fetch(context.Context, contracts.MonitorTarget) (providers.WeatherBundle, error) {
	if s.err != nil {
		return providers.WeatherBundle{}, s.err
	}
	return s.bundle, nil
}

type stubAirQualityClient struct {
	current  contracts.AirQualityCurrent
	forecast contracts.AirQualityForecast
	err      error
}

func (s stubAirQualityClient) FetchCurrent(context.Context, contracts.MonitorTarget) (contracts.AirQualityCurrent, error) {
	if s.err != nil {
		return contracts.AirQualityCurrent{}, s.err
	}
	return s.current, nil
}

func (s stubAirQualityClient) FetchForecast(context.Context, contracts.MonitorTarget) (contracts.AirQualityForecast, error) {
	if s.err != nil {
		return contracts.AirQualityForecast{}, s.err
	}
	return s.forecast, nil
}

func newTestStack(t *testing.T, cfg config.Config, forecastClient providers.ForecastClient) (*storage.Repository, *core.Service, *api.Server, *mcp.Server) {
	return newTestStackWithClients(t, cfg, stubWeatherClient{}, forecastClient, stubAirQualityClient{})
}

func newTestStackWithClients(t *testing.T, cfg config.Config, weatherClient providers.WeatherClient, forecastClient providers.ForecastClient, airClient providers.AirQualityClient) (*storage.Repository, *core.Service, *api.Server, *mcp.Server) {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	repo := storage.NewRepository(db, storage.DialectSQLite)
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	svc := core.NewService(cfg, repo, collector.NewSimulatorService(cfg), weatherClient, forecastClient, airClient)
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

func TestTargetsEndpointReturnsConfiguredTargets(t *testing.T) {
	cfg := baseConfig()
	cfg.Auth = config.AuthConfig{
		Tokens: []config.TokenConfig{
			{Name: "telemetry", Value: "telemetry-token", Scopes: []string{auth.ScopeReadTelemetry}},
		},
	}
	cfg.Monitoring = config.MonitoringConfig{
		DefaultTargetID: "home",
		Targets: []config.MonitorTargetConfig{
			{ID: "home", DisplayName: "Home", Latitude: 1, Longitude: 1, IncludeIndoor: true, EnableWeather: true, EnableAirQuality: true},
			{ID: "cabin", DisplayName: "Cabin", Latitude: 2, Longitude: 2, EnableWeather: true, EnableAirQuality: true},
		},
	}

	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})

	req := httptest.NewRequest(http.MethodGet, "/v1/targets", nil)
	req.Header.Set("Authorization", "Bearer telemetry-token")
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var payload []contracts.MonitorTarget
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response json decode failed: %v", err)
	}
	if len(payload) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(payload))
	}
	if payload[0].ID != "home" || !payload[0].Default {
		t.Fatalf("expected home target to be default, got %+v", payload[0])
	}
}

func TestWeatherCurrentEndpointUsesTargetQuery(t *testing.T) {
	cfg := baseConfig()
	cfg.Auth = config.AuthConfig{
		Tokens: []config.TokenConfig{
			{Name: "telemetry", Value: "telemetry-token", Scopes: []string{auth.ScopeReadTelemetry}},
		},
	}
	cfg.Monitoring = config.MonitoringConfig{
		DefaultTargetID: "home",
		Targets: []config.MonitorTargetConfig{
			{ID: "home", DisplayName: "Home", Latitude: 1, Longitude: 1, IncludeIndoor: true, EnableWeather: true, EnableAirQuality: true},
			{ID: "cabin", DisplayName: "Cabin", Latitude: 2, Longitude: 2, EnableWeather: true, EnableAirQuality: true},
		},
	}

	repo, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	if err := repo.StoreWeatherCurrent(context.Background(), contracts.WeatherCurrent{
		TargetID:      "cabin",
		Source:        "noaa",
		RecordedAt:    time.Now().UTC(),
		FetchedAt:     time.Now().UTC(),
		Condition:     "Clear",
		TemperatureC:  8.4,
		HumidityPct:   42,
		WindSpeedMS:   1.2,
		Quality:       contracts.QualityGood,
		SourceStation: "KTEST",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/weather/current?target=cabin", nil)
	req.Header.Set("Authorization", "Bearer telemetry-token")
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var payload contracts.WeatherCurrent
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response json decode failed: %v", err)
	}
	if payload.TargetID != "cabin" {
		t.Fatalf("expected target cabin, got %q", payload.TargetID)
	}
}

func TestLiveEndpointSendsInitialSnapshot(t *testing.T) {
	cfg := baseConfig()
	cfg.Auth = config.AuthConfig{
		Tokens: []config.TokenConfig{
			{Name: "telemetry", Value: "telemetry-token", Scopes: []string{auth.ScopeReadTelemetry}},
		},
	}
	cfg.Monitoring = config.MonitoringConfig{
		DefaultTargetID: "home",
		Targets: []config.MonitorTargetConfig{
			{ID: "home", DisplayName: "Home", Latitude: 1, Longitude: 1, IncludeIndoor: true, EnableWeather: true, EnableAirQuality: true},
		},
	}

	repo, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	now := time.Now().UTC()
	if err := repo.StoreWeatherCurrent(context.Background(), contracts.WeatherCurrent{
		TargetID:     "home",
		Source:       "noaa",
		RecordedAt:   now,
		FetchedAt:    now,
		Condition:    "Sunny",
		TemperatureC: 12.3,
		Quality:      contracts.QualityGood,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertForecast(context.Background(), contracts.ForecastSnapshot{
		TargetID:   "home",
		StationID:  "home",
		Provider:   "noaa",
		Latitude:   1,
		Longitude:  1,
		FetchedAt:  now,
		FreshUntil: now.Add(time.Hour),
		Points:     []contracts.ForecastPoint{{Time: now.Add(time.Hour), TemperatureC: 13}},
	}); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/live?target=home"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Authorization": []string{"Bearer telemetry-token"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var payload struct {
		Type     string                    `json:"type"`
		Snapshot contracts.ClimateSnapshot `json:"snapshot"`
	}
	if err := conn.ReadJSON(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Type != "snapshot_full" {
		t.Fatalf("expected snapshot_full message, got %q", payload.Type)
	}
	if payload.Snapshot.TargetID != "home" {
		t.Fatalf("expected snapshot for home, got %q", payload.Snapshot.TargetID)
	}
}
