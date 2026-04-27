package contracts

import "time"

type QualityFlag string

const (
	QualityGood        QualityFlag = "good"
	QualityEstimated   QualityFlag = "estimated"
	QualityOutlier     QualityFlag = "outlier"
	QualityUnavailable QualityFlag = "unavailable"
)

// Live event type constants (A-3).
const (
	LiveEventSnapshotFull      = "snapshot_full"
	LiveEventReadingUpdated    = "reading_updated"
	LiveEventWeatherUpdated    = "weather_updated"
	LiveEventAirQualityUpdated = "air_quality_updated"
	LiveEventAlertAdded        = "alert_added"
	LiveEventAlertCleared      = "alert_cleared"
	LiveEventProviderChanged   = "provider_status_changed"
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

// ForecastPoint is a single hour in a weather forecast (A-2: added WindDirectionDeg,
// PrecipitationProbabilityPct; PrecipMM is now reserved for actual precipitation amount).
type ForecastPoint struct {
	Time                        time.Time `json:"time"`
	TemperatureC                float64   `json:"temperature_c"`
	HumidityPct                 float64   `json:"humidity_pct"`
	WindSpeedMS                 float64   `json:"wind_speed_ms"`
	WindDirectionDeg            float64   `json:"wind_direction_deg,omitempty"`
	PrecipMM                    float64   `json:"precip_mm"`
	PrecipitationProbabilityPct int       `json:"precip_probability_pct,omitempty"`
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

// SourceLicense describes license and retention requirements for a data provider (A-5).
type SourceLicense struct {
	Provider            string `json:"provider"`
	License             string `json:"license"`
	AttributionRequired bool   `json:"attribution_required"`
	LicenseURL          string `json:"license_url,omitempty"`
	RetentionDays       int    `json:"retention_days,omitempty"`
	Notes               string `json:"notes,omitempty"`
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

// AstronomyContext holds daily solar event times for a target location (C-4).
type AstronomyContext struct {
	Sunrise            time.Time `json:"sunrise"`
	Sunset             time.Time `json:"sunset"`
	CivilTwilightBegin time.Time `json:"civil_twilight_begin"`
	CivilTwilightEnd   time.Time `json:"civil_twilight_end"`
	SolarNoon          time.Time `json:"solar_noon"`
	DayLengthMin       float64   `json:"day_length_min"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// WildfireContext reports nearby fire hotspot activity from NASA FIRMS (C-1).
type WildfireContext struct {
	NearestHotspotKm       float64   `json:"nearest_hotspot_km"`
	ActiveHotspotsInRadius int       `json:"active_hotspots_in_radius"`
	RiskLevel              string    `json:"risk_level"` // none, low, moderate, high, extreme
	RadiusKm               float64   `json:"radius_km"`
	Source                 string    `json:"source"`
	UpdatedAt              time.Time `json:"updated_at"`
}

// PollenContext holds pollen index values for a target location (C-2).
type PollenContext struct {
	TreeIndex  int       `json:"tree_index"`
	GrassIndex int       `json:"grass_index"`
	WeedIndex  int       `json:"weed_index"`
	Category   string    `json:"category"`
	Source     string    `json:"source"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// UVContext holds the UV index for a target location (C-3).
type UVContext struct {
	UVIndex   float64   `json:"uv_index"`
	Category  string    `json:"uv_category"`
	Source    string    `json:"source"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PurpleAirAQ holds neighborhood-level PM data from PurpleAir sensors (C-6).
type PurpleAirAQ struct {
	PM25Avg     float64   `json:"pm25_avg"`
	PM10Avg     float64   `json:"pm10_avg"`
	SensorCount int       `json:"sensor_count"`
	RadiusKm    float64   `json:"radius_km"`
	Source      string    `json:"source"`
	UpdatedAt   time.Time `json:"updated_at"`
	Stale       bool      `json:"stale"`
	LastError   string    `json:"last_error,omitempty"`
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
	// Contextual enrichments (Phase C).
	Astronomy *AstronomyContext `json:"astronomy,omitempty"`
	Wildfire  *WildfireContext  `json:"wildfire,omitempty"`
	Pollen    *PollenContext    `json:"pollen,omitempty"`
	UV        *UVContext        `json:"uv,omitempty"`
	PurpleAir *PurpleAirAQ     `json:"purple_air,omitempty"`
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

// SLOBreach records a single freshness SLO violation for a target+component (X-3).
type SLOBreach struct {
	TargetID    string     `json:"target_id"`
	Component   string     `json:"component"`   // "indoor", "weather", "air_quality"
	MaxAgeS     int        `json:"max_age_s"`   // configured threshold
	ActualAgeS  int        `json:"actual_age_s"` // how old the data actually is
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
}

// DiagnosticsReport is the response shape for GET /v1/diagnostics/data-gaps (X-3).
// It replaces the bare []DataGap so SLO breach information can be included.
type DiagnosticsReport struct {
	DataGaps    []DataGap   `json:"data_gaps"`
	SLOBreaches []SLOBreach `json:"slo_breaches"`
	GeneratedAt time.Time   `json:"generated_at"`
}

// CommandStatus tracks the lifecycle of a submitted command (D-1).
type CommandStatus string

const (
	CommandStatusPending    CommandStatus = "pending"
	CommandStatusAccepted   CommandStatus = "accepted"
	CommandStatusRejected   CommandStatus = "rejected"
	CommandStatusExecuting  CommandStatus = "executing"
	CommandStatusSucceeded  CommandStatus = "succeeded"
	CommandStatusFailed     CommandStatus = "failed"
	CommandStatusExpired    CommandStatus = "expired"
)

// CommandActor identifies who or what submitted a command (D-1).
type CommandActor struct {
	Kind string `json:"kind"` // "user", "agent", "scheduler"
	ID   string `json:"id"`   // token name, agent ID, or scheduler name
}

// Command is a write-side intent record — a request to change the state of a
// device or target. Commands are append-only; results are stored separately (D-1).
type Command struct {
	CommandID       string        `json:"command_id"`
	TargetID        string        `json:"target_id"`
	DeviceID        string        `json:"device_id,omitempty"`
	Capability      string        `json:"capability"`              // e.g. "set_temperature", "set_mode"
	Arguments       map[string]any `json:"arguments,omitempty"`    // capability-specific params
	Actor           CommandActor  `json:"actor"`
	IdempotencyKey  string        `json:"idempotency_key,omitempty"`
	Status          CommandStatus `json:"status"`
	RequestedAt     time.Time     `json:"requested_at"`
	ExpiresAt       *time.Time    `json:"expires_at,omitempty"`
}

// CommandResult records the outcome of a Command (D-1).
type CommandResult struct {
	CommandID                  string        `json:"command_id"`
	Status                     CommandStatus `json:"status"`
	AcceptedAt                 *time.Time    `json:"accepted_at,omitempty"`
	ProviderAcknowledgedAt     *time.Time    `json:"provider_acknowledged_at,omitempty"`
	ObservedEffect             string        `json:"observed_effect,omitempty"`
	FinalStatus                CommandStatus `json:"final_status,omitempty"`
	Error                      string        `json:"error,omitempty"`
	UpdatedAt                  time.Time     `json:"updated_at"`
}

// ConsentGrant records that a specific integration is active and documents what
// data it produces, how long it is kept, and how it may be shared (X-4).
type ConsentGrant struct {
	ID                  string     `json:"id"`
	TargetID            string     `json:"target_id"`           // monitoring target, or "*" for station-wide
	Provider            string     `json:"provider"`            // e.g. "noaa", "airthings"
	AccountSubject      string     `json:"account_subject,omitempty"` // device/account identifier
	GrantedScopes       []string   `json:"granted_scopes"`      // data access scopes granted
	DataClasses         []string   `json:"data_classes"`        // types of data produced
	RetentionDays       int        `json:"retention_days"`      // 0 = follow global config
	ShareWithAgents     bool       `json:"share_with_agents"`
	ShareWithDashboards bool       `json:"share_with_dashboards"`
	LicenseRequirements string     `json:"license_requirements,omitempty"`
	GrantedAt           time.Time  `json:"granted_at"`
	RevokedAt           *time.Time `json:"revoked_at,omitempty"`
}
