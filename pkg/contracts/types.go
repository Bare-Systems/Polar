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
	TargetID    string          `json:"target_id,omitempty"`
	StationID   string          `json:"station_id,omitempty"`
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
	StationID           string           `json:"station_id"`
	SupportedMetrics    []string         `json:"supported_metrics"`
	SupportsForecast    bool             `json:"supports_forecast"`
	SupportsCalibration bool             `json:"supports_calibration"`
	MinSamplingSeconds  int              `json:"min_sampling_seconds"`
	MaxSamplingSeconds  int              `json:"max_sampling_seconds"`
	Targets             []MonitorTarget  `json:"targets,omitempty"`
	ProviderSupport     []ProviderTarget `json:"provider_support,omitempty"`
}

type ProviderTarget struct {
	TargetID   string `json:"target_id"`
	Weather    bool   `json:"weather"`
	AirQuality bool   `json:"air_quality"`
	Indoor     bool   `json:"indoor"`
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

type MonitorTarget struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name"`
	Latitude    float64  `json:"latitude"`
	Longitude   float64  `json:"longitude"`
	ZipCode     string   `json:"zip_code,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	Indoor      bool     `json:"indoor"`
	Weather     bool     `json:"weather"`
	AirQuality  bool     `json:"air_quality"`
	Default     bool     `json:"default"`
}

type ProviderStatus struct {
	TargetID      string     `json:"target_id"`
	Provider      string     `json:"provider"`
	Component     string     `json:"component"`
	Status        string     `json:"status"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt *time.Time `json:"last_failure_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	FreshUntil    *time.Time `json:"fresh_until,omitempty"`
	Stale         bool       `json:"stale"`
}

type WeatherCurrent struct {
	TargetID         string      `json:"target_id"`
	Source           string      `json:"source"`
	RecordedAt       time.Time   `json:"recorded_at"`
	FetchedAt        time.Time   `json:"fetched_at"`
	Condition        string      `json:"condition"`
	TemperatureC     float64     `json:"temperature_c"`
	HumidityPct      float64     `json:"humidity_pct"`
	WindSpeedMS      float64     `json:"wind_speed_ms"`
	WindDirectionDeg float64     `json:"wind_direction_deg,omitempty"`
	PressureHPa      float64     `json:"pressure_hpa,omitempty"`
	VisibilityKM     float64     `json:"visibility_km,omitempty"`
	SourceStation    string      `json:"source_station,omitempty"`
	Quality          QualityFlag `json:"quality"`
	Stale            bool        `json:"stale"`
	StaleReason      string      `json:"stale_reason,omitempty"`
	LastError        string      `json:"last_error,omitempty"`
}

type AirQualityPollutant struct {
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	AQI      int     `json:"aqi,omitempty"`
	Category string  `json:"category,omitempty"`
	Value    float64 `json:"value,omitempty"`
	Unit     string  `json:"unit,omitempty"`
	Primary  bool    `json:"primary"`
}

type AirQualityCurrent struct {
	TargetID      string                `json:"target_id"`
	Source        string                `json:"source"`
	RecordedAt    time.Time             `json:"recorded_at"`
	FetchedAt     time.Time             `json:"fetched_at"`
	ReportingArea string                `json:"reporting_area,omitempty"`
	StateCode     string                `json:"state_code,omitempty"`
	OverallAQI    int                   `json:"overall_aqi"`
	Category      string                `json:"category"`
	Pollutants    []AirQualityPollutant `json:"pollutants"`
	Stale         bool                  `json:"stale"`
	StaleReason   string                `json:"stale_reason,omitempty"`
	LastError     string                `json:"last_error,omitempty"`
}

type AirQualityForecastPeriod struct {
	Date       string `json:"date"`
	Parameter  string `json:"parameter"`
	AQI        int    `json:"aqi"`
	Category   string `json:"category"`
	ActionDay  bool   `json:"action_day"`
	Discussion string `json:"discussion,omitempty"`
}

type AirQualityForecast struct {
	TargetID    string                     `json:"target_id"`
	Source      string                     `json:"source"`
	FetchedAt   time.Time                  `json:"fetched_at"`
	Periods     []AirQualityForecastPeriod `json:"periods"`
	Stale       bool                       `json:"stale"`
	StaleReason string                     `json:"stale_reason,omitempty"`
	LastError   string                     `json:"last_error,omitempty"`
}

type WeatherAlert struct {
	ID          string     `json:"id"`
	TargetID    string     `json:"target_id"`
	Source      string     `json:"source"`
	Event       string     `json:"event"`
	Severity    string     `json:"severity,omitempty"`
	Urgency     string     `json:"urgency,omitempty"`
	Headline    string     `json:"headline,omitempty"`
	Description string     `json:"description,omitempty"`
	Areas       []string   `json:"areas,omitempty"`
	StartsAt    *time.Time `json:"starts_at,omitempty"`
	EndsAt      *time.Time `json:"ends_at,omitempty"`
	SentAt      *time.Time `json:"sent_at,omitempty"`
}

// ClimateMetric is a normalized, display-ready environmental metric.
type ClimateMetric struct {
	Name         string      `json:"name"`
	DisplayName  string      `json:"display_name"`
	Value        float64     `json:"value"`
	Unit         string      `json:"unit"`
	DisplayValue string      `json:"display_value"`
	Domain       string      `json:"domain"`
	Source       string      `json:"source"`
	Quality      QualityFlag `json:"quality"`
	RecordedAt   time.Time   `json:"recorded_at"`
}

type IndoorClimate struct {
	Sources       []string        `json:"sources"`
	Readings      []ClimateMetric `json:"readings"`
	LastReadingAt *time.Time      `json:"last_reading_at,omitempty"`
	Stale         bool            `json:"stale"`
}

type OutdoorClimate struct {
	Target          MonitorTarget       `json:"target"`
	Sources         []string            `json:"sources"`
	CurrentWeather  *WeatherCurrent     `json:"current_weather,omitempty"`
	Forecast        *ForecastSnapshot   `json:"forecast,omitempty"`
	AirQuality      *AirQualityCurrent  `json:"air_quality,omitempty"`
	AirQualityTrend *AirQualityForecast `json:"air_quality_forecast,omitempty"`
	Alerts          []WeatherAlert      `json:"alerts,omitempty"`
	Statuses        []ProviderStatus    `json:"provider_statuses,omitempty"`
	Current         []ClimateMetric     `json:"current,omitempty"`
	LastFetchedAt   *time.Time          `json:"last_fetched_at,omitempty"`
	FreshUntil      *time.Time          `json:"fresh_until,omitempty"`
	Stale           bool                `json:"stale"`
}

type ClimateSnapshot struct {
	StationID   string         `json:"station_id"`
	TargetID    string         `json:"target_id"`
	GeneratedAt time.Time      `json:"generated_at"`
	Indoor      IndoorClimate  `json:"indoor"`
	Outdoor     OutdoorClimate `json:"outdoor"`
}

type LiveUpdate struct {
	Type      string          `json:"type"`
	TargetID  string          `json:"target_id"`
	Timestamp time.Time       `json:"timestamp"`
	Snapshot  ClimateSnapshot `json:"snapshot"`
}
