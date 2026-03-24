package contracts

import "time"

type QualityFlag string

const (
	QualityGood        QualityFlag = "good"
	QualityEstimated   QualityFlag = "estimated"
	QualityOutlier     QualityFlag = "outlier"
	QualityUnavailable QualityFlag = "unavailable"
)

type Reading struct {
	StationID   string      `json:"station_id"`
	SensorID    string      `json:"sensor_id"`
	Metric      string      `json:"metric"`
	Value       float64     `json:"value"`
	Unit        string      `json:"unit"`
	Source      string      `json:"source"`
	QualityFlag QualityFlag `json:"quality_flag"`
	RecordedAt  time.Time   `json:"recorded_at"`
	ReceivedAt  time.Time   `json:"received_at"`
}

type ForecastPoint struct {
	Time         time.Time `json:"time"`
	TemperatureC float64   `json:"temperature_c"`
	HumidityPct  float64   `json:"humidity_pct"`
	WindSpeedMS  float64   `json:"wind_speed_ms"`
	PrecipMM     float64   `json:"precip_mm"`
}

type ForecastSnapshot struct {
	StationID   string          `json:"station_id"`
	Provider    string          `json:"provider"`
	Latitude    float64         `json:"latitude"`
	Longitude   float64         `json:"longitude"`
	FetchedAt   time.Time       `json:"fetched_at"`
	FreshUntil  time.Time       `json:"fresh_until"`
	Points      []ForecastPoint `json:"points"`
	Stale       bool            `json:"stale"`
	StaleReason string          `json:"stale_reason,omitempty"`
	LastError   string          `json:"last_error,omitempty"`
}

type CapabilityDescriptor struct {
	StationID           string   `json:"station_id"`
	SupportedMetrics    []string `json:"supported_metrics"`
	SupportsForecast    bool     `json:"supports_forecast"`
	SupportsCalibration bool     `json:"supports_calibration"`
	MinSamplingSeconds  int      `json:"min_sampling_seconds"`
	MaxSamplingSeconds  int      `json:"max_sampling_seconds"`
}

type ComponentHealth struct {
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	LastSuccess time.Time `json:"last_success"`
	Message     string    `json:"message"`
}

type StationHealth struct {
	StationID   string            `json:"station_id"`
	Overall     string            `json:"overall"`
	Components  []ComponentHealth `json:"components"`
	GeneratedAt time.Time         `json:"generated_at"`
}

type ReadinessStatus struct {
	StationID   string            `json:"station_id"`
	Ready       bool              `json:"ready"`
	Status      string            `json:"status"`
	Components  []ComponentHealth `json:"components"`
	GeneratedAt time.Time         `json:"generated_at"`
}

type DataGap struct {
	Metric      string    `json:"metric"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	MissingForS int       `json:"missing_for_seconds"`
}

type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	TraceID   string `json:"trace_id"`
}

// ClimateMetric is a single normalized, display-ready environmental metric.
// It is source-agnostic: the underlying data may come from Airthings, a home
// weather station, a thermostat, or an NWS feed — the shape is always the same.
type ClimateMetric struct {
	Name         string      `json:"name"`          // canonical key, e.g. "temperature"
	DisplayName  string      `json:"display_name"`  // human label, e.g. "Temperature"
	Value        float64     `json:"value"`
	Unit         string      `json:"unit"`          // e.g. "°C", "%RH", "ppm", "Bq/m³"
	DisplayValue string      `json:"display_value"` // formatted, e.g. "21.4 °C"
	Domain       string      `json:"domain"`        // "air_quality" | "thermal" | "comfort" | "weather" | "other"
	Source       string      `json:"source"`        // e.g. "airthings", "open_meteo", "thermostat"
	Quality      QualityFlag `json:"quality"`
	RecordedAt   time.Time   `json:"recorded_at"`
}

// IndoorClimate groups all readings taken inside the home.
// Sources will grow over time to include thermostats, HVAC sensors, etc.
type IndoorClimate struct {
	Sources       []string        `json:"sources"`
	Readings      []ClimateMetric `json:"readings"`
	LastReadingAt *time.Time      `json:"last_reading_at,omitempty"`
	Stale         bool            `json:"stale"`
}

// OutdoorClimate groups current conditions and forecast for outside the home.
// Sources will grow to include home weather stations, NWS, and other providers.
type OutdoorClimate struct {
	Sources       []string        `json:"sources"`
	Current       []ClimateMetric `json:"current"`
	Forecast      []ForecastPoint `json:"forecast,omitempty"`
	LastFetchedAt *time.Time      `json:"last_fetched_at,omitempty"`
	FreshUntil    *time.Time      `json:"fresh_until,omitempty"`
	Stale         bool            `json:"stale"`
}

// ClimateSnapshot is the top-level aggregated climate response. It presents
// indoor and outdoor conditions as a unified picture regardless of which
// underlying integrations (Airthings, HVAC, NWS, etc.) supplied the data.
type ClimateSnapshot struct {
	StationID   string         `json:"station_id"`
	GeneratedAt time.Time      `json:"generated_at"`
	Indoor      IndoorClimate  `json:"indoor"`
	Outdoor     OutdoorClimate `json:"outdoor"`
}
