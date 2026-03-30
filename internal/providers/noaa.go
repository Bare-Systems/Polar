package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"polar/pkg/contracts"
)

type WeatherBundle struct {
	Current  contracts.WeatherCurrent
	Forecast contracts.ForecastSnapshot
	Alerts   []contracts.WeatherAlert
}

type WeatherClient interface {
	Fetch(ctx context.Context, target contracts.MonitorTarget) (WeatherBundle, error)
}

type NOAAClient struct {
	baseURL   string
	userAgent string
	http      *http.Client

	mu    sync.RWMutex
	cache map[string]noaaPointMetadata
}

type noaaPointMetadata struct {
	ForecastHourlyURL   string
	ObservationStations string
	City                string
	State               string
}

func NewNOAAClient(baseURL, userAgent string, httpClient *http.Client) *NOAAClient {
	return &NOAAClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		userAgent: userAgent,
		http:      httpClient,
		cache:     make(map[string]noaaPointMetadata),
	}
}

func (c *NOAAClient) Fetch(ctx context.Context, target contracts.MonitorTarget) (WeatherBundle, error) {
	meta, err := c.pointMetadata(ctx, target)
	if err != nil {
		return WeatherBundle{}, err
	}

	current, err := c.fetchCurrent(ctx, target, meta)
	if err != nil {
		return WeatherBundle{}, err
	}
	forecast, err := c.fetchForecast(ctx, target, meta)
	if err != nil {
		return WeatherBundle{}, err
	}
	alerts, err := c.fetchAlerts(ctx, target)
	if err != nil {
		return WeatherBundle{}, err
	}

	return WeatherBundle{
		Current:  current,
		Forecast: forecast,
		Alerts:   alerts,
	}, nil
}

func (c *NOAAClient) pointMetadata(ctx context.Context, target contracts.MonitorTarget) (noaaPointMetadata, error) {
	c.mu.RLock()
	if meta, ok := c.cache[target.ID]; ok {
		c.mu.RUnlock()
		return meta, nil
	}
	c.mu.RUnlock()

	reqURL := fmt.Sprintf("%s/points/%0.4f,%0.4f", c.baseURL, target.Latitude, target.Longitude)
	var payload struct {
		Properties struct {
			ForecastHourly      string `json:"forecastHourly"`
			ObservationStations string `json:"observationStations"`
			RelativeLocation    struct {
				Properties struct {
					City  string `json:"city"`
					State string `json:"state"`
				} `json:"properties"`
			} `json:"relativeLocation"`
		} `json:"properties"`
	}
	if err := c.getJSON(ctx, reqURL, &payload); err != nil {
		return noaaPointMetadata{}, err
	}
	meta := noaaPointMetadata{
		ForecastHourlyURL:   payload.Properties.ForecastHourly,
		ObservationStations: payload.Properties.ObservationStations,
		City:                payload.Properties.RelativeLocation.Properties.City,
		State:               payload.Properties.RelativeLocation.Properties.State,
	}
	c.mu.Lock()
	c.cache[target.ID] = meta
	c.mu.Unlock()
	return meta, nil
}

func (c *NOAAClient) fetchCurrent(ctx context.Context, target contracts.MonitorTarget, meta noaaPointMetadata) (contracts.WeatherCurrent, error) {
	var stations struct {
		Features []struct {
			Properties struct {
				StationIdentifier string `json:"stationIdentifier"`
			} `json:"properties"`
		} `json:"features"`
	}
	if err := c.getJSON(ctx, meta.ObservationStations, &stations); err != nil {
		return contracts.WeatherCurrent{}, err
	}
	if len(stations.Features) == 0 {
		return contracts.WeatherCurrent{}, fmt.Errorf("noaa observation stations unavailable")
	}
	stationID := stations.Features[0].Properties.StationIdentifier
	reqURL := fmt.Sprintf("%s/stations/%s/observations/latest", c.baseURL, stationID)

	var payload struct {
		Properties struct {
			Timestamp       string `json:"timestamp"`
			TextDescription string `json:"textDescription"`
			Temperature     struct {
				Value *float64 `json:"value"`
			} `json:"temperature"`
			RelativeHumidity struct {
				Value *float64 `json:"value"`
			} `json:"relativeHumidity"`
			WindSpeed struct {
				Value *float64 `json:"value"`
			} `json:"windSpeed"`
			WindDirection struct {
				Value *float64 `json:"value"`
			} `json:"windDirection"`
			BarometricPressure struct {
				Value *float64 `json:"value"`
			} `json:"barometricPressure"`
			Visibility struct {
				Value *float64 `json:"value"`
			} `json:"visibility"`
		} `json:"properties"`
	}
	if err := c.getJSON(ctx, reqURL, &payload); err != nil {
		return contracts.WeatherCurrent{}, err
	}

	recordedAt, _ := time.Parse(time.RFC3339, payload.Properties.Timestamp)
	current := contracts.WeatherCurrent{
		TargetID:      target.ID,
		Source:        "noaa",
		RecordedAt:    recordedAt.UTC(),
		FetchedAt:     time.Now().UTC(),
		Condition:     payload.Properties.TextDescription,
		SourceStation: stationID,
		Quality:       contracts.QualityGood,
	}
	if payload.Properties.Temperature.Value != nil {
		current.TemperatureC = *payload.Properties.Temperature.Value
	}
	if payload.Properties.RelativeHumidity.Value != nil {
		current.HumidityPct = *payload.Properties.RelativeHumidity.Value
	}
	if payload.Properties.WindSpeed.Value != nil {
		current.WindSpeedMS = *payload.Properties.WindSpeed.Value
	}
	if payload.Properties.WindDirection.Value != nil {
		current.WindDirectionDeg = *payload.Properties.WindDirection.Value
	}
	if payload.Properties.BarometricPressure.Value != nil {
		current.PressureHPa = *payload.Properties.BarometricPressure.Value / 100
	}
	if payload.Properties.Visibility.Value != nil {
		current.VisibilityKM = *payload.Properties.Visibility.Value / 1000
	}
	return current, nil
}

func (c *NOAAClient) fetchForecast(ctx context.Context, target contracts.MonitorTarget, meta noaaPointMetadata) (contracts.ForecastSnapshot, error) {
	reqURL, err := url.Parse(meta.ForecastHourlyURL)
	if err != nil {
		return contracts.ForecastSnapshot{}, err
	}
	q := reqURL.Query()
	q.Set("units", "si")
	reqURL.RawQuery = q.Encode()

	var payload struct {
		Properties struct {
			Periods []struct {
				StartTime        string  `json:"startTime"`
				Temperature      float64 `json:"temperature"`
				TemperatureUnit  string  `json:"temperatureUnit"`
				WindSpeed        string  `json:"windSpeed"`
				ShortForecast    string  `json:"shortForecast"`
				RelativeHumidity struct {
					Value *float64 `json:"value"`
				} `json:"relativeHumidity"`
				ProbabilityOfPrecipitation struct {
					Value *float64 `json:"value"`
				} `json:"probabilityOfPrecipitation"`
			} `json:"periods"`
		} `json:"properties"`
	}
	if err := c.getJSON(ctx, reqURL.String(), &payload); err != nil {
		return contracts.ForecastSnapshot{}, err
	}

	points := make([]contracts.ForecastPoint, 0, len(payload.Properties.Periods))
	for _, period := range payload.Properties.Periods {
		tm, err := time.Parse(time.RFC3339, period.StartTime)
		if err != nil {
			continue
		}
		point := contracts.ForecastPoint{
			Time:         tm.UTC(),
			TemperatureC: temperatureToC(period.Temperature, period.TemperatureUnit),
			WindSpeedMS:  parseWindSpeed(period.WindSpeed),
		}
		if period.RelativeHumidity.Value != nil {
			point.HumidityPct = *period.RelativeHumidity.Value
		}
		if period.ProbabilityOfPrecipitation.Value != nil {
			point.PrecipMM = *period.ProbabilityOfPrecipitation.Value
		}
		points = append(points, point)
	}

	return contracts.ForecastSnapshot{
		TargetID:   target.ID,
		StationID:  target.ID,
		Provider:   "noaa",
		Latitude:   target.Latitude,
		Longitude:  target.Longitude,
		FetchedAt:  time.Now().UTC(),
		FreshUntil: time.Now().UTC().Add(2 * time.Hour),
		Points:     points,
	}, nil
}

func (c *NOAAClient) fetchAlerts(ctx context.Context, target contracts.MonitorTarget) ([]contracts.WeatherAlert, error) {
	reqURL := fmt.Sprintf("%s/alerts/active?point=%0.4f,%0.4f", c.baseURL, target.Latitude, target.Longitude)
	var payload struct {
		Features []struct {
			ID         string `json:"id"`
			Properties struct {
				Event       string `json:"event"`
				Severity    string `json:"severity"`
				Urgency     string `json:"urgency"`
				Headline    string `json:"headline"`
				Description string `json:"description"`
				AreaDesc    string `json:"areaDesc"`
				Effective   string `json:"effective"`
				Ends        string `json:"ends"`
				Sent        string `json:"sent"`
			} `json:"properties"`
		} `json:"features"`
	}
	if err := c.getJSON(ctx, reqURL, &payload); err != nil {
		return nil, err
	}

	alerts := make([]contracts.WeatherAlert, 0, len(payload.Features))
	for _, feature := range payload.Features {
		alert := contracts.WeatherAlert{
			ID:          feature.ID,
			TargetID:    target.ID,
			Source:      "noaa",
			Event:       feature.Properties.Event,
			Severity:    feature.Properties.Severity,
			Urgency:     feature.Properties.Urgency,
			Headline:    feature.Properties.Headline,
			Description: feature.Properties.Description,
		}
		if feature.Properties.AreaDesc != "" {
			alert.Areas = splitAndTrim(feature.Properties.AreaDesc, ";")
		}
		if t, err := time.Parse(time.RFC3339, feature.Properties.Effective); err == nil {
			tt := t.UTC()
			alert.StartsAt = &tt
		}
		if t, err := time.Parse(time.RFC3339, feature.Properties.Ends); err == nil {
			tt := t.UTC()
			alert.EndsAt = &tt
		}
		if t, err := time.Parse(time.RFC3339, feature.Properties.Sent); err == nil {
			tt := t.UTC()
			alert.SentAt = &tt
		}
		alerts = append(alerts, alert)
	}
	return alerts, nil
}

func (c *NOAAClient) getJSON(ctx context.Context, reqURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/geo+json, application/ld+json;q=0.9, application/json;q=0.8")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("noaa status: %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func temperatureToC(value float64, unit string) float64 {
	switch strings.ToUpper(strings.TrimSpace(unit)) {
	case "F":
		return (value - 32) * 5 / 9
	default:
		return value
	}
}

func parseWindSpeed(raw string) float64 {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return 0
	}
	value, err := strconv.ParseFloat(strings.TrimSuffix(fields[0], "-"), 64)
	if err != nil {
		value = parseWindRange(fields[0])
	}
	unit := "km/h"
	if len(fields) > 1 {
		unit = strings.ToLower(fields[1])
	}
	switch unit {
	case "mph":
		return value * 0.44704
	case "km/h", "kmh":
		return value / 3.6
	default:
		return value
	}
}

func parseWindRange(raw string) float64 {
	if !strings.Contains(raw, "-") {
		return 0
	}
	parts := strings.SplitN(raw, "-", 2)
	if len(parts) != 2 {
		return 0
	}
	a, errA := strconv.ParseFloat(parts[0], 64)
	b, errB := strconv.ParseFloat(parts[1], 64)
	if errA != nil || errB != nil {
		return 0
	}
	return (a + b) / 2
}

func splitAndTrim(raw, sep string) []string {
	parts := strings.Split(raw, sep)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}
