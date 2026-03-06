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
	StationID string          `json:"station_id"`
	Provider  string          `json:"provider"`
	Latitude  float64         `json:"latitude"`
	Longitude float64         `json:"longitude"`
	FetchedAt time.Time       `json:"fetched_at"`
	Points    []ForecastPoint `json:"points"`
	Stale     bool            `json:"stale"`
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
