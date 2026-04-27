// contract_test.go — X-1: Contract fixture test suite.
//
// Each test:
//  1. Spins up a fake HTTP server serving recorded provider fixtures.
//  2. Calls the real provider/collector code against it.
//  3. Asserts that the normalized contract fields satisfy baseline invariants
//     (no zero-value required fields, valid quality flags, sane numeric ranges).
package test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"polar/internal/collector"
	"polar/internal/config"
	"polar/internal/providers"
	"polar/pkg/contracts"
)

// --- fixture helpers ---

// fixtureBody reads a file from test/fixtures/ and replaces {SERVER_URL} with
// the supplied test server URL. Fatals the test on I/O error.
func fixtureBody(t *testing.T, name, serverURL string) string {
	t.Helper()
	b, err := os.ReadFile("fixtures/" + name)
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return strings.ReplaceAll(string(b), "{SERVER_URL}", serverURL)
}

func serveJSON(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

// --- contract invariant helpers ---

func assertForecastSnapshot(t *testing.T, snap contracts.ForecastSnapshot, provider string) {
	t.Helper()
	if snap.Provider != provider {
		t.Errorf("provider: want %q, got %q", provider, snap.Provider)
	}
	if snap.FetchedAt.IsZero() {
		t.Error("fetched_at must not be zero")
	}
	if len(snap.Points) == 0 {
		t.Fatal("points must not be empty")
	}
	for i, p := range snap.Points {
		if p.Time.IsZero() {
			t.Errorf("point[%d].time is zero", i)
		}
		if p.TemperatureC == 0 {
			t.Errorf("point[%d].temperature_c is zero", i)
		}
		if p.PrecipitationProbabilityPct < 0 || p.PrecipitationProbabilityPct > 100 {
			t.Errorf("point[%d].precip_probability_pct out of range: %d", i, p.PrecipitationProbabilityPct)
		}
		if p.WindDirectionDeg < 0 || p.WindDirectionDeg > 360 {
			t.Errorf("point[%d].wind_direction_deg out of range: %f", i, p.WindDirectionDeg)
		}
	}
}

func assertWeatherCurrent(t *testing.T, cur contracts.WeatherCurrent) {
	t.Helper()
	if cur.Source == "" {
		t.Error("source must not be empty")
	}
	if cur.TargetID == "" {
		t.Error("target_id must not be empty")
	}
	if cur.FetchedAt.IsZero() {
		t.Error("fetched_at must not be zero")
	}
	if cur.RecordedAt.IsZero() {
		t.Error("recorded_at must not be zero")
	}
	if cur.Condition == "" {
		t.Error("condition must not be empty")
	}
	if cur.TemperatureC == 0 {
		t.Error("temperature_c must not be zero")
	}
	if cur.Quality == "" {
		t.Error("quality must not be empty")
	}
}

func assertAirQualityCurrent(t *testing.T, aq contracts.AirQualityCurrent) {
	t.Helper()
	if aq.Source == "" {
		t.Error("source must not be empty")
	}
	if aq.TargetID == "" {
		t.Error("target_id must not be empty")
	}
	if aq.OverallAQI < 0 {
		t.Error("overall_aqi must be >= 0")
	}
	if aq.Category == "" {
		t.Error("category must not be empty")
	}
	if len(aq.Pollutants) == 0 {
		t.Error("pollutants must not be empty")
	}
	hasPrimary := false
	for _, p := range aq.Pollutants {
		if p.Primary {
			hasPrimary = true
		}
		if p.Code == "" {
			t.Error("pollutant code must not be empty")
		}
	}
	if !hasPrimary {
		t.Error("at least one pollutant must be marked primary")
	}
}

func assertReadings(t *testing.T, readings []contracts.Reading, source string) {
	t.Helper()
	if len(readings) == 0 {
		t.Fatalf("%s: no readings returned", source)
	}
	for _, r := range readings {
		if r.Source != source {
			t.Errorf("reading source: want %q, got %q", source, r.Source)
		}
		if r.Metric == "" {
			t.Error("reading metric must not be empty")
		}
		if r.Unit == "" {
			t.Error("reading unit must not be empty")
		}
		if r.RecordedAt.IsZero() {
			t.Error("reading recorded_at must not be zero")
		}
		validFlags := map[contracts.QualityFlag]bool{
			contracts.QualityGood:        true,
			contracts.QualityEstimated:   true,
			contracts.QualityOutlier:     true,
			contracts.QualityUnavailable: true,
		}
		if !validFlags[r.QualityFlag] {
			t.Errorf("invalid quality_flag: %q", r.QualityFlag)
		}
	}
}

// --- X-1.1: NOAA provider ---

func TestNOAAContractFixture(t *testing.T) {
	// Handlers are registered after server creation so we can embed server.URL.
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/points/"):
			serveJSON(fixtureBody(t, "noaa_points.json", srv.URL))(w, r)
		case r.URL.Path == "/stations" || strings.HasSuffix(r.URL.Path, "/stations"):
			serveJSON(fixtureBody(t, "noaa_stations.json", srv.URL))(w, r)
		case strings.HasSuffix(r.URL.Path, "/observations/latest"):
			serveJSON(fixtureBody(t, "noaa_current_obs.json", srv.URL))(w, r)
		case strings.Contains(r.URL.Path, "/forecast/hourly"):
			serveJSON(fixtureBody(t, "noaa_forecast_hourly.json", srv.URL))(w, r)
		case strings.HasPrefix(r.URL.Path, "/alerts/"):
			serveJSON(fixtureBody(t, "noaa_alerts.json", srv.URL))(w, r)
		default:
			t.Logf("NOAA test server: unhandled path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := providers.NewNOAAClient(srv.URL, "polar-test/1.0", srv.Client())
	target := contracts.MonitorTarget{
		ID:         "test",
		Latitude:   38.9072,
		Longitude:  -77.0369,
		Weather:    true,
		AirQuality: true,
	}

	bundle, err := client.Fetch(context.Background(), target)
	if err != nil {
		t.Fatalf("NOAA Fetch failed: %v", err)
	}

	t.Run("current", func(t *testing.T) {
		assertWeatherCurrent(t, bundle.Current)
		if bundle.Current.SourceStation != "KTEST" {
			t.Errorf("source_station: want KTEST, got %q", bundle.Current.SourceStation)
		}
		// Pressure converted from Pa to hPa: 101325 Pa / 100 = 1013.25 hPa
		if bundle.Current.PressureHPa < 900 || bundle.Current.PressureHPa > 1100 {
			t.Errorf("pressure_hpa out of plausible range: %f", bundle.Current.PressureHPa)
		}
	})

	t.Run("forecast", func(t *testing.T) {
		assertForecastSnapshot(t, bundle.Forecast, "noaa")
		// Verify cardinal wind direction converted to degrees (NW → 315).
		if bundle.Forecast.Points[0].WindDirectionDeg != 315 {
			t.Errorf("wind_direction_deg for NW: want 315, got %f", bundle.Forecast.Points[0].WindDirectionDeg)
		}
		// Precipitation probability parsed correctly (20%, not misused as mm).
		if bundle.Forecast.Points[0].PrecipitationProbabilityPct != 20 {
			t.Errorf("precip_probability_pct: want 20, got %d", bundle.Forecast.Points[0].PrecipitationProbabilityPct)
		}
	})

	t.Run("alerts", func(t *testing.T) {
		if len(bundle.Alerts) == 0 {
			t.Fatal("expected at least one alert")
		}
		a := bundle.Alerts[0]
		if a.Event == "" {
			t.Error("alert event must not be empty")
		}
		if a.Severity == "" {
			t.Error("alert severity must not be empty")
		}
		if len(a.Areas) == 0 {
			t.Error("alert areas must not be empty")
		}
		if a.StartsAt == nil {
			t.Error("alert starts_at must not be nil")
		}
	})
}

// --- X-1.2: Open-Meteo provider ---

func TestOpenMeteoContractFixture(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveJSON(fixtureBody(t, "openmeteo_forecast.json", ""))(w, r)
	}))
	defer srv.Close()

	client := providers.NewOpenMeteoClient(srv.Client())
	target := contracts.MonitorTarget{ID: "test", Latitude: 38.9, Longitude: -77.0}

	snap, err := client.Fetch(context.Background(), target, srv.URL)
	if err != nil {
		t.Fatalf("Open-Meteo Fetch failed: %v", err)
	}

	assertForecastSnapshot(t, snap, "open-meteo")

	// Verify all three new fields are mapped: wind direction, precip prob, precip mm.
	p := snap.Points[0]
	if p.WindDirectionDeg != 280.0 {
		t.Errorf("wind_direction_deg: want 280, got %f", p.WindDirectionDeg)
	}
	if p.PrecipitationProbabilityPct != 5 {
		t.Errorf("precip_probability_pct: want 5, got %d", p.PrecipitationProbabilityPct)
	}
	if p.PrecipMM != 0.0 {
		t.Errorf("precip_mm: want 0.0, got %f", p.PrecipMM)
	}
}

// --- X-1.3: AirNow provider ---

func TestAirNowContractFixture(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/observation/"):
			serveJSON(fixtureBody(t, "airnow_current.json", srv.URL))(w, r)
		case strings.Contains(r.URL.Path, "/forecast/"):
			serveJSON(fixtureBody(t, "airnow_forecast.json", srv.URL))(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := providers.NewAirNowClient(srv.URL, "test-key", srv.Client())
	target := contracts.MonitorTarget{ID: "test", Latitude: 38.9, Longitude: -77.0}

	t.Run("current", func(t *testing.T) {
		cur, err := client.FetchCurrent(context.Background(), target)
		if err != nil {
			t.Fatalf("AirNow FetchCurrent failed: %v", err)
		}
		assertAirQualityCurrent(t, cur)
		// PM2.5 should be primary (highest AQI in fixture is 42).
		if cur.OverallAQI != 42 {
			t.Errorf("overall_aqi: want 42, got %d", cur.OverallAQI)
		}
		if cur.ReportingArea == "" {
			t.Error("reporting_area must not be empty")
		}
	})

	t.Run("forecast", func(t *testing.T) {
		fc, err := client.FetchForecast(context.Background(), target)
		if err != nil {
			t.Fatalf("AirNow FetchForecast failed: %v", err)
		}
		if fc.Source != "airnow" {
			t.Errorf("source: want airnow, got %q", fc.Source)
		}
		if len(fc.Periods) == 0 {
			t.Fatal("periods must not be empty")
		}
		for _, p := range fc.Periods {
			if p.Date == "" {
				t.Error("period date must not be empty")
			}
			if p.Parameter == "" {
				t.Error("period parameter must not be empty")
			}
		}
		// Fixture has two dates; verify they come back sorted ascending.
		if fc.Periods[0].Date > fc.Periods[1].Date {
			t.Errorf("periods not sorted ascending: %q > %q", fc.Periods[0].Date, fc.Periods[1].Date)
		}
	})
}

// --- X-1.4: Shelly collector ---

func TestShellyContractFixture(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "Temperature.GetStatus"):
			serveJSON(fixtureBody(t, "shelly_temperature.json", ""))(w, r)
		case strings.Contains(r.URL.Path, "Humidity.GetStatus"):
			serveJSON(fixtureBody(t, "shelly_humidity.json", ""))(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Strip http:// — ShellyService prepends it when building request URLs.
	deviceIP := strings.TrimPrefix(srv.URL, "http://")

	cfg := baseConfig()
	cfg.Shelly = config.ShellyConfig{
		Devices: []config.ShellyDevice{
			{ID: "dev1", IP: deviceIP, Label: "living-room", Enabled: true},
		},
	}
	svc := collector.NewShellyService(cfg, srv.Client())

	readings := svc.Collect()
	assertReadings(t, readings, "shelly")

	metrics := map[string]float64{}
	for _, r := range readings {
		metrics[r.Metric] = r.Value
	}
	if metrics["temperature"] != 22.4 {
		t.Errorf("temperature: want 22.4, got %f", metrics["temperature"])
	}
	if metrics["humidity"] != 48.0 {
		t.Errorf("humidity: want 48.0, got %f", metrics["humidity"])
	}
}

// --- X-1.5: SwitchBot collector ---

func TestSwitchBotContractFixture(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveJSON(fixtureBody(t, "switchbot_status.json", ""))(w, r)
	}))
	defer srv.Close()

	cfg := baseConfig()
	cfg.SwitchBot = config.SwitchBotConfig{
		Token:  "test-token",
		Secret: "test-secret",
		Devices: []config.SwitchBotDevice{
			{DeviceID: "AABBCCDDEEFF", DeviceType: "Meter", Label: "bedroom", Enabled: true},
		},
	}
	svc := collector.NewSwitchBotService(cfg, srv.Client())
	svc.SetBaseURL(srv.URL)

	readings := svc.Collect()
	assertReadings(t, readings, "switchbot")

	metrics := map[string]float64{}
	for _, r := range readings {
		metrics[r.Metric] = r.Value
	}
	if metrics["temperature"] != 21.5 {
		t.Errorf("temperature: want 21.5, got %f", metrics["temperature"])
	}
	if metrics["humidity"] != 52.0 {
		t.Errorf("humidity: want 52.0, got %f", metrics["humidity"])
	}
}
