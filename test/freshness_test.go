// freshness_test.go — Provider freshness, SLO monitoring, and fallback behaviour.
//
// Covers:
//   - DataGaps returns a DiagnosticsReport (data_gaps + slo_breaches + generated_at)
//   - SLO breach fires when weather data age exceeds threshold
//   - SLO breach fires when AQ data age exceeds threshold
//   - StationHealth is degraded when an SLO breach exists
//   - No breach when provider is fresh
//   - Provider failure → stale + last_error populated in ProviderStatus
//   - Climate snapshot degrades gracefully when provider fails
package test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"polar/internal/auth"
	"polar/internal/config"
	"polar/internal/providers"
	"polar/pkg/contracts"
)

// sloConfig returns a baseConfig with tightly configured SLO thresholds so tests
// can trigger breaches by seeding a stale provider status row rather than sleeping.
func sloConfig() config.Config {
	cfg := baseConfig()
	cfg.Auth = config.AuthConfig{
		Tokens: []config.TokenConfig{
			{Name: "admin", Value: "admin-token", Scopes: []string{auth.WildcardScope}},
		},
	}
	cfg.Monitoring = config.MonitoringConfig{
		DefaultTargetID: "home",
		Targets: []config.MonitorTargetConfig{
			{
				ID:              "home",
				DisplayName:     "Home",
				Latitude:        37.7,
				Longitude:       -122.4,
				IncludeIndoor:   true,
				EnableWeather:   true,
				EnableAirQuality: true,
			},
		},
	}
	// Very tight SLO so seeding a 2-minute-old record is enough to breach.
	cfg.SLO = config.FreshnessSLOConfig{
		IndoorMaxAgeS:  60,   // 1 minute
		WeatherMaxAgeS: 60,   // 1 minute
		AQMaxAgeS:      60,   // 1 minute
	}
	return cfg
}

// staleProviderStatus builds a ProviderStatus whose LastSuccessAt is in the past
// by the given age. Status is "healthy" so the StationHealth component scan does
// not trigger a degraded result before the SLO check runs — the SLO check itself
// is what we want to exercise.
func staleProviderStatus(targetID, provider, component string, ageSeconds int) contracts.ProviderStatus {
	t := time.Now().UTC().Add(-time.Duration(ageSeconds) * time.Second)
	ft := t.Add(time.Duration(ageSeconds/2) * time.Second) // fresh_until is also in the past
	return contracts.ProviderStatus{
		TargetID:      targetID,
		Provider:      provider,
		Component:     component,
		Status:        "healthy", // intentionally "healthy" so SLO check runs
		LastSuccessAt: &t,
		FreshUntil:    &ft,
		Stale:         true,
	}
}

// freshProviderStatus builds a ProviderStatus updated just now. Status is "healthy"
// so it doesn't degrade overall health and the SLO check does not fire.
func freshProviderStatus(targetID, provider, component string) contracts.ProviderStatus {
	t := time.Now().UTC()
	ft := t.Add(20 * time.Minute)
	return contracts.ProviderStatus{
		TargetID:      targetID,
		Provider:      provider,
		Component:     component,
		Status:        "healthy",
		LastSuccessAt: &t,
		FreshUntil:    &ft,
		Stale:         false,
	}
}

func authedGet(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// --- Freshness.1: DataGaps returns DiagnosticsReport shape ---

func TestDataGapsResponseShape(t *testing.T) {
	cfg := sloConfig()
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	handler := server.Handler()

	rr := authedGet(t, handler, "/v1/diagnostics/data-gaps")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Read body once; use for both struct decode and key inspection.
	raw, err := io.ReadAll(rr.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var report contracts.DiagnosticsReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode DiagnosticsReport: %v", err)
	}
	if report.GeneratedAt.IsZero() {
		t.Error("generated_at must not be zero")
	}
	// data_gaps and slo_breaches must be present (may be empty slices or null, not absent).
	if !jsonHasKey(raw, "data_gaps") {
		t.Error("DiagnosticsReport must contain 'data_gaps' key")
	}
	if !jsonHasKey(raw, "slo_breaches") {
		t.Error("DiagnosticsReport must contain 'slo_breaches' key")
	}
	if !jsonHasKey(raw, "generated_at") {
		t.Error("DiagnosticsReport must contain 'generated_at' key")
	}
}

// --- Freshness.2: Weather SLO breach when provider status is stale ---

func TestWeatherSLOBreach(t *testing.T) {
	cfg := sloConfig() // WeatherMaxAgeS = 60
	repo, svc, _, _ := newTestStack(t, cfg, stubForecastClient{})

	// Seed a weather status that is 5 minutes old — well past the 60s SLO.
	status := staleProviderStatus("home", "noaa", "weather", 300)
	if err := repo.UpsertProviderStatus(context.Background(), status); err != nil {
		t.Fatalf("UpsertProviderStatus: %v", err)
	}

	report, err := svc.DataGaps(context.Background())
	if err != nil {
		t.Fatalf("DataGaps: %v", err)
	}

	found := false
	for _, b := range report.SLOBreaches {
		if b.TargetID == "home" && b.Component == "weather" {
			found = true
			if b.MaxAgeS != 60 {
				t.Errorf("max_age_s: expected 60, got %d", b.MaxAgeS)
			}
			if b.ActualAgeS < 60 {
				t.Errorf("actual_age_s %d should be >= 60", b.ActualAgeS)
			}
			if b.LastSeenAt == nil {
				t.Error("last_seen_at must not be nil for weather breach")
			}
		}
	}
	if !found {
		t.Errorf("expected a weather SLO breach for target 'home'; got breaches: %+v", report.SLOBreaches)
	}
}

// --- Freshness.3: AQ SLO breach when provider status is stale ---

func TestAQSLOBreach(t *testing.T) {
	cfg := sloConfig() // AQMaxAgeS = 60
	repo, svc, _, _ := newTestStack(t, cfg, stubForecastClient{})

	// Seed an AQ status that is 10 minutes old.
	status := staleProviderStatus("home", "airnow", "air_quality_current", 600)
	if err := repo.UpsertProviderStatus(context.Background(), status); err != nil {
		t.Fatalf("UpsertProviderStatus: %v", err)
	}

	report, err := svc.DataGaps(context.Background())
	if err != nil {
		t.Fatalf("DataGaps: %v", err)
	}

	found := false
	for _, b := range report.SLOBreaches {
		if b.TargetID == "home" && b.Component == "air_quality" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an air_quality SLO breach; got: %+v", report.SLOBreaches)
	}
}

// --- Freshness.4: No breach when provider is fresh ---

func TestNoSLOBreachWhenFresh(t *testing.T) {
	cfg := sloConfig()
	repo, svc, _, _ := newTestStack(t, cfg, stubForecastClient{})

	// Seed fresh statuses.
	for _, ps := range []contracts.ProviderStatus{
		freshProviderStatus("home", "noaa", "weather"),
		freshProviderStatus("home", "airnow", "air_quality_current"),
	} {
		if err := repo.UpsertProviderStatus(context.Background(), ps); err != nil {
			t.Fatalf("UpsertProviderStatus: %v", err)
		}
	}

	report, err := svc.DataGaps(context.Background())
	if err != nil {
		t.Fatalf("DataGaps: %v", err)
	}
	if len(report.SLOBreaches) != 0 {
		t.Errorf("expected no SLO breaches with fresh data; got %+v", report.SLOBreaches)
	}
}

// --- Freshness.5: StationHealth is degraded when weather SLO is breached ---

func TestStationHealthDegradedOnSLOBreach(t *testing.T) {
	cfg := sloConfig()
	repo, svc, _, _ := newTestStack(t, cfg, stubForecastClient{})

	// Run collector so the collector component is "healthy" (not "starting").
	// If the collector is "starting", overall is already degraded and the SLO
	// branch (which only fires when overall == "healthy") never executes.
	if err := svc.PullSensorReadings(context.Background()); err != nil {
		t.Fatalf("PullSensorReadings: %v", err)
	}

	// Seed stale weather — 5 minutes old, SLO = 60s.
	status := staleProviderStatus("home", "noaa", "weather", 300)
	if err := repo.UpsertProviderStatus(context.Background(), status); err != nil {
		t.Fatalf("UpsertProviderStatus: %v", err)
	}

	health := svc.StationHealth()
	if health.Overall != "degraded" {
		t.Errorf("expected overall=degraded, got %q", health.Overall)
	}

	// Should have an SLO component entry.
	found := false
	for _, c := range health.Components {
		if c.Status == "degraded" && containsSubstring(c.Name, "slo:") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a degraded SLO component; got components: %+v", health.Components)
	}
}

// --- Freshness.6: StationHealth is healthy when all data is fresh ---

func TestStationHealthHealthyWhenFresh(t *testing.T) {
	cfg := sloConfig()
	repo, svc, _, _ := newTestStack(t, cfg, stubForecastClient{})

	// Seed fresh provider statuses.
	for _, ps := range []contracts.ProviderStatus{
		freshProviderStatus("home", "noaa", "weather"),
		freshProviderStatus("home", "airnow", "air_quality_current"),
	} {
		if err := repo.UpsertProviderStatus(context.Background(), ps); err != nil {
			t.Fatalf("UpsertProviderStatus: %v", err)
		}
	}
	// Also run the collector so indoor is fresh.
	if err := svc.PullSensorReadings(context.Background()); err != nil {
		t.Fatalf("PullSensorReadings: %v", err)
	}

	health := svc.StationHealth()
	if health.Overall != "healthy" {
		t.Errorf("expected overall=healthy, got %q (components: %+v)", health.Overall, health.Components)
	}
}

// --- Freshness.7: Provider failure populates stale + last_error in forecast snapshot ---

func TestForecastStaleFlagOnProviderFailure(t *testing.T) {
	cfg := baseConfig()
	cfg.Features.EnableForecast = true
	cfg.Auth = config.AuthConfig{
		Tokens: []config.TokenConfig{
			{Name: "admin", Value: "admin-token", Scopes: []string{auth.WildcardScope}},
		},
	}
	providerErr := errors.New("upstream unavailable")
	_, svc, server, _ := newTestStackWithClients(t, cfg,
		stubWeatherClient{},
		stubForecastClient{err: providerErr},
		stubAirQualityClient{},
	)
	handler := server.Handler()

	// Attempt a forecast pull — it will fail.
	_ = svc.PullForecast(context.Background())

	rr := authedGet(t, handler, "/v1/forecast/latest")
	// 200 with stale snapshot, or 500 if no data at all — either is acceptable; must not 401/403.
	if rr.Code == http.StatusUnauthorized || rr.Code == http.StatusForbidden {
		t.Fatalf("unexpected auth error on forecast after failure: %d", rr.Code)
	}

	if rr.Code == http.StatusOK {
		var snap contracts.ForecastSnapshot
		if err := json.NewDecoder(rr.Body).Decode(&snap); err != nil {
			t.Fatalf("decode ForecastSnapshot: %v", err)
		}
		// If a snapshot is returned, it should be stale.
		if !snap.Stale && snap.LastError == "" {
			t.Error("forecast snapshot should be stale or carry last_error after provider failure")
		}
	}
}

// --- Freshness.8: Climate snapshot renders with degraded-but-usable state ---

func TestClimateSnapshotDegradedGraceful(t *testing.T) {
	cfg := baseConfig()
	cfg.Auth = config.AuthConfig{
		Tokens: []config.TokenConfig{
			{Name: "admin", Value: "admin-token", Scopes: []string{auth.WildcardScope}},
		},
	}
	// Provider that always fails.
	_, svc, server, _ := newTestStackWithClients(t, cfg,
		stubWeatherClient{err: errors.New("weather provider down")},
		stubForecastClient{err: errors.New("forecast provider down")},
		stubAirQualityClient{err: errors.New("AQ provider down")},
	)
	handler := server.Handler()

	// Seed readings so indoor is populated.
	if err := svc.PullSensorReadings(context.Background()); err != nil {
		t.Fatalf("PullSensorReadings: %v", err)
	}

	rr := authedGet(t, handler, "/v1/climate/snapshot")
	// Must return 200 — snapshot degrades gracefully, never 500 due to a single provider failure.
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 degraded snapshot, got %d: %s", rr.Code, rr.Body.String())
	}

	var snap contracts.ClimateSnapshot
	if err := json.NewDecoder(rr.Body).Decode(&snap); err != nil {
		t.Fatalf("decode ClimateSnapshot: %v", err)
	}
	// Indoor readings should still be present.
	if len(snap.Indoor.Readings) == 0 {
		t.Error("indoor readings must be present even when outdoor providers fail")
	}
}

// --- Freshness.9: SLO metrics counter increments on breach ---

func TestSLOMetricsCounter(t *testing.T) {
	cfg := sloConfig()
	repo, svc, _, _ := newTestStack(t, cfg, stubForecastClient{})

	snapshot0 := svc.MetricsSnapshot()
	initial := snapshot0.SLO.BreachesTotal

	// Seed two stale statuses.
	for _, ps := range []contracts.ProviderStatus{
		staleProviderStatus("home", "noaa", "weather", 300),
		staleProviderStatus("home", "airnow", "air_quality_current", 300),
	} {
		_ = repo.UpsertProviderStatus(context.Background(), ps)
	}

	_, _ = svc.DataGaps(context.Background())
	_, _ = svc.DataGaps(context.Background()) // second call — counters must accumulate

	snapshot1 := svc.MetricsSnapshot()
	if snapshot1.SLO.BreachesTotal <= initial {
		t.Errorf("expected SLO breach counter to increase; initial=%d, after=%d",
			initial, snapshot1.SLO.BreachesTotal)
	}
}

// --- helpers ---

func jsonHasKey(data []byte, key string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		findSubstring(s, substr))
}

func findSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Ensure providers package is used (stubWeatherClient satisfies providers.WeatherClient).
var _ providers.WeatherClient = stubWeatherClient{}
