package config

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"strconv"
	"strings"
	"time"

	"polar/pkg/contracts"
)

type Config struct {
	Profile    string           `json:"profile"`
	Station    StationConfig    `json:"station"`
	Server     ServerConfig     `json:"server"`
	Storage    StorageConfig    `json:"storage"`
	Auth       AuthConfig       `json:"auth"`
	Features   FeatureFlags     `json:"features"`
	Polling    PollingConfig    `json:"polling"`
	Retention  RetentionConfig  `json:"retention"`
	SLO        FreshnessSLOConfig `json:"slo"`
	Provider   ProviderConfig   `json:"provider"`
	Airthings  AirthingsConfig  `json:"airthings"`
	Shelly     ShellyConfig     `json:"shelly"`
	SwitchBot  SwitchBotConfig  `json:"switchbot"`
	Netatmo    NetatmoConfig    `json:"netatmo"`
	Monitoring MonitoringConfig `json:"monitoring"`
}

type StationConfig struct {
	ID        string  `json:"id"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type ServerConfig struct {
	ListenAddr    string `json:"listen_addr"`
	MCPListenAddr string `json:"mcp_listen_addr"`
}

type StorageConfig struct {
	Driver      string `json:"driver"`
	SQLitePath  string `json:"sqlite_path"`
	DatabaseURL string `json:"database_url"`
}

type AuthConfig struct {
	ServiceToken string        `json:"service_token"`
	Tokens       []TokenConfig `json:"tokens"`
}

// TokenConfig describes a named bearer token with scopes and optional expiry (A-6).
// AllowedTargets restricts which monitoring targets the token may access (X-5).
// An empty slice means all targets are allowed (backward-compatible default).
type TokenConfig struct {
	Name           string     `json:"name"`
	Value          string     `json:"value"`
	Scopes         []string   `json:"scopes"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	AllowedTargets []string   `json:"allowed_targets,omitempty"`
}

type FeatureFlags struct {
	EnableForecast  bool `json:"enable_forecast"`
	EnableMCP       bool `json:"enable_mcp"`
	EnableAirthings bool `json:"enable_airthings"`
	EnableLive      bool `json:"enable_live"`
	EnableShelly    bool `json:"enable_shelly"`
	EnableSwitchBot bool `json:"enable_switchbot"`
	EnableNetatmo   bool `json:"enable_netatmo"`
	EnableAstronomy bool `json:"enable_astronomy"`
	EnableWildfire  bool `json:"enable_wildfire"`
	EnablePollen    bool `json:"enable_pollen"`
	EnableUV        bool `json:"enable_uv"`
	EnablePurpleAir bool `json:"enable_purple_air"`
}

type PollingConfig struct {
	SensorInterval     time.Duration `json:"sensor_interval"`
	ForecastInterval   time.Duration `json:"forecast_interval"`
	WeatherInterval    time.Duration `json:"weather_interval"`
	AirQualityInterval time.Duration `json:"air_quality_interval"`
	AlertInterval      time.Duration `json:"alert_interval"`
	WildfireInterval   time.Duration `json:"wildfire_interval"`
	PollenInterval     time.Duration `json:"pollen_interval"`
	PurpleAirInterval  time.Duration `json:"purple_air_interval"`
}

// RetentionConfig controls how long old data rows are kept (A-1).
type RetentionConfig struct {
	OutdoorSnapshotDays int `json:"outdoor_snapshot_days"`
	AlertDays           int `json:"alert_days"`
	AuditDays           int `json:"audit_days"`
	CommandDays         int `json:"command_days"` // terminal commands; 0 = 30 days
}

// FreshnessSLOConfig defines per-component maximum acceptable data ages (X-3).
// A breach is reported when the most recent successful pull is older than the
// configured threshold. Zero means the SLO is disabled for that component.
type FreshnessSLOConfig struct {
	IndoorMaxAgeS  int `json:"indoor_max_age_s"`  // default 300  (5 min)
	WeatherMaxAgeS int `json:"weather_max_age_s"` // default 1200 (20 min)
	AQMaxAgeS      int `json:"aq_max_age_s"`      // default 3600 (1 hr)
}

type ProviderConfig struct {
	OpenMeteoURL    string `json:"open_meteo_url"`
	NOAABaseURL     string `json:"noaa_base_url"`
	NOAAUserAgent   string `json:"noaa_user_agent"`
	AirNowURL       string `json:"airnow_url"`
	AirNowToken     string `json:"airnow_token"`
	FIRMSAPIKey     string `json:"firms_api_key"`
	FIRMSRadiusKm   float64 `json:"firms_radius_km"`
	WeatherAPIKey   string `json:"weatherapi_key"`
	PurpleAirAPIKey string `json:"purpleair_read_key"`
	PurpleAirRadius float64 `json:"purpleair_radius_km"`
}

type AirthingsConfig struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	DeviceIDs    []string `json:"device_ids"`
}

// ShellyConfig describes a set of local Shelly devices to poll (B-1).
type ShellyConfig struct {
	Devices []ShellyDevice `json:"devices"`
}

type ShellyDevice struct {
	ID      string `json:"id"`
	IP      string `json:"ip"`
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
}

// SwitchBotConfig describes SwitchBot OpenAPI credentials and target devices (B-2).
type SwitchBotConfig struct {
	Token   string          `json:"token"`
	Secret  string          `json:"secret"`
	Devices []SwitchBotDevice `json:"devices"`
}

type SwitchBotDevice struct {
	DeviceID   string `json:"device_id"`
	DeviceType string `json:"device_type"`
	Label      string `json:"label"`
	Enabled    bool   `json:"enabled"`
}

// NetatmoConfig describes Netatmo OAuth2 credentials (C-5).
type NetatmoConfig struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	RefreshToken string   `json:"refresh_token"`
	DeviceIDs    []string `json:"device_ids"`
}

type MonitoringConfig struct {
	DefaultTargetID string                `json:"default_target_id"`
	Targets         []MonitorTargetConfig `json:"targets"`
}

type MonitorTargetConfig struct {
	ID               string   `json:"id"`
	DisplayName      string   `json:"display_name"`
	Latitude         float64  `json:"latitude"`
	Longitude        float64  `json:"longitude"`
	ZipCode          string   `json:"zip_code"`
	Labels           []string `json:"labels"`
	IncludeIndoor    bool     `json:"include_indoor"`
	EnableWeather    bool     `json:"enable_weather"`
	EnableAirQuality bool     `json:"enable_air_quality"`
}

func defaults() Config {
	return Config{
		Profile: "simulator",
		Station: StationConfig{ID: "polar-dev", Latitude: 40.7128, Longitude: -74.0060},
		Server:  ServerConfig{ListenAddr: ":8080", MCPListenAddr: ":8081"},
		Storage: StorageConfig{
			Driver:     "sqlite",
			SQLitePath: "./polar.db",
		},
		Auth:     AuthConfig{ServiceToken: "dev-token"},
		Features: FeatureFlags{
			EnableForecast:  true,
			EnableMCP:       true,
			EnableLive:      true,
			EnableAstronomy: true,
		},
		Polling: PollingConfig{
			SensorInterval:     10 * time.Second,
			ForecastInterval:   30 * time.Minute,
			WeatherInterval:    15 * time.Minute,
			AirQualityInterval: 30 * time.Minute,
			AlertInterval:      5 * time.Minute,
			WildfireInterval:   60 * time.Minute,
			PollenInterval:     60 * time.Minute,
			PurpleAirInterval:  30 * time.Minute,
		},
		Retention: RetentionConfig{
			OutdoorSnapshotDays: 90,
			AlertDays:           30,
			AuditDays:           90,
			CommandDays:         30,
		},
		SLO: FreshnessSLOConfig{
			IndoorMaxAgeS:  300,
			WeatherMaxAgeS: 1200,
			AQMaxAgeS:      3600,
		},
		Provider: ProviderConfig{
			OpenMeteoURL:  "https://api.open-meteo.com/v1/forecast",
			NOAABaseURL:   "https://api.weather.gov",
			NOAAUserAgent: "Polar/0.2 (ops@baresystems.local)",
			AirNowURL:     "https://www.airnowapi.org/aq",
			FIRMSRadiusKm: 50,
			PurpleAirRadius: 5,
		},
	}
}

func Load(args []string) (Config, error) {
	cfg := defaults()
	fs := flag.NewFlagSet("polar", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to JSON config file")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}

	if *configPath != "" {
		b, err := os.ReadFile(*configPath)
		if err != nil {
			return cfg, err
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			return cfg, err
		}
	}

	applyEnv(&cfg)
	cfg.Monitoring = normalizeMonitoring(cfg)
	if err := validate(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("POLAR_PROFILE"); v != "" {
		cfg.Profile = v
	}
	if v := os.Getenv("POLAR_STATION_ID"); v != "" {
		cfg.Station.ID = v
	}
	if v := os.Getenv("POLAR_LAT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Station.Latitude = f
		}
	}
	if v := os.Getenv("POLAR_LON"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Station.Longitude = f
		}
	}
	if v := os.Getenv("POLAR_LISTEN_ADDR"); v != "" {
		cfg.Server.ListenAddr = v
	}
	if v := os.Getenv("POLAR_MCP_ADDR"); v != "" {
		cfg.Server.MCPListenAddr = v
	}
	if v := os.Getenv("POLAR_STORAGE_DRIVER"); v != "" {
		cfg.Storage.Driver = v
	}
	if v := os.Getenv("POLAR_SQLITE_PATH"); v != "" {
		cfg.Storage.SQLitePath = v
	}
	if v := os.Getenv("POLAR_DATABASE_URL"); v != "" {
		cfg.Storage.DatabaseURL = v
	}
	if v := os.Getenv("POLAR_SERVICE_TOKEN"); v != "" {
		cfg.Auth.ServiceToken = v
	}
	if v := os.Getenv("POLAR_AUTH_TOKENS_JSON"); v != "" {
		var tokens []TokenConfig
		if err := json.Unmarshal([]byte(v), &tokens); err == nil {
			cfg.Auth.Tokens = tokens
		}
	}
	if v := os.Getenv("POLAR_ENABLE_FORECAST"); v != "" {
		cfg.Features.EnableForecast = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("POLAR_ENABLE_MCP"); v != "" {
		cfg.Features.EnableMCP = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("POLAR_ENABLE_AIRTHINGS"); v != "" {
		cfg.Features.EnableAirthings = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("POLAR_ENABLE_LIVE"); v != "" {
		cfg.Features.EnableLive = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("POLAR_ENABLE_SHELLY"); v != "" {
		cfg.Features.EnableShelly = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("POLAR_ENABLE_SWITCHBOT"); v != "" {
		cfg.Features.EnableSwitchBot = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("POLAR_ENABLE_NETATMO"); v != "" {
		cfg.Features.EnableNetatmo = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("POLAR_ENABLE_ASTRONOMY"); v != "" {
		cfg.Features.EnableAstronomy = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("POLAR_ENABLE_WILDFIRE"); v != "" {
		cfg.Features.EnableWildfire = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("POLAR_ENABLE_POLLEN"); v != "" {
		cfg.Features.EnablePollen = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("POLAR_ENABLE_UV"); v != "" {
		cfg.Features.EnableUV = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("POLAR_ENABLE_PURPLEAIR"); v != "" {
		cfg.Features.EnablePurpleAir = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("POLAR_SENSOR_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Polling.SensorInterval = d
		}
	}
	if v := os.Getenv("POLAR_FORECAST_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Polling.ForecastInterval = d
		}
	}
	if v := os.Getenv("POLAR_WEATHER_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Polling.WeatherInterval = d
		}
	}
	if v := os.Getenv("POLAR_AIR_QUALITY_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Polling.AirQualityInterval = d
		}
	}
	if v := os.Getenv("POLAR_ALERT_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Polling.AlertInterval = d
		}
	}
	if v := os.Getenv("POLAR_WILDFIRE_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Polling.WildfireInterval = d
		}
	}
	if v := os.Getenv("POLAR_POLLEN_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Polling.PollenInterval = d
		}
	}
	if v := os.Getenv("POLAR_PURPLEAIR_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Polling.PurpleAirInterval = d
		}
	}
	if v := os.Getenv("POLAR_SLO_INDOOR_MAX_AGE_S"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.SLO.IndoorMaxAgeS = n
		}
	}
	if v := os.Getenv("POLAR_SLO_WEATHER_MAX_AGE_S"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.SLO.WeatherMaxAgeS = n
		}
	}
	if v := os.Getenv("POLAR_SLO_AQ_MAX_AGE_S"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.SLO.AQMaxAgeS = n
		}
	}
	if v := os.Getenv("POLAR_RETENTION_SNAPSHOT_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Retention.OutdoorSnapshotDays = n
		}
	}
	if v := os.Getenv("POLAR_RETENTION_ALERT_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Retention.AlertDays = n
		}
	}
	if v := os.Getenv("POLAR_RETENTION_AUDIT_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Retention.AuditDays = n
		}
	}
	if v := os.Getenv("POLAR_RETENTION_COMMAND_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Retention.CommandDays = n
		}
	}
	if v := os.Getenv("POLAR_OPEN_METEO_URL"); v != "" {
		cfg.Provider.OpenMeteoURL = v
	}
	if v := os.Getenv("POLAR_NOAA_BASE_URL"); v != "" {
		cfg.Provider.NOAABaseURL = v
	}
	if v := os.Getenv("POLAR_NOAA_USER_AGENT"); v != "" {
		cfg.Provider.NOAAUserAgent = v
	}
	if v := os.Getenv("POLAR_AIRNOW_URL"); v != "" {
		cfg.Provider.AirNowURL = v
	}
	if v := os.Getenv("POLAR_AIRNOW_TOKEN"); v != "" {
		cfg.Provider.AirNowToken = v
	}
	if v := os.Getenv("POLAR_FIRMS_API_KEY"); v != "" {
		cfg.Provider.FIRMSAPIKey = v
	}
	if v := os.Getenv("POLAR_FIRMS_RADIUS_KM"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Provider.FIRMSRadiusKm = f
		}
	}
	if v := os.Getenv("POLAR_WEATHERAPI_KEY"); v != "" {
		cfg.Provider.WeatherAPIKey = v
	}
	if v := os.Getenv("POLAR_PURPLEAIR_READ_KEY"); v != "" {
		cfg.Provider.PurpleAirAPIKey = v
	}
	if v := os.Getenv("POLAR_PURPLEAIR_RADIUS_KM"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Provider.PurpleAirRadius = f
		}
	}
	if v := os.Getenv("POLAR_AIRTHINGS_CLIENT_ID"); v != "" {
		cfg.Airthings.ClientID = v
	}
	if v := os.Getenv("POLAR_AIRTHINGS_CLIENT_SECRET"); v != "" {
		cfg.Airthings.ClientSecret = v
	}
	if v := os.Getenv("POLAR_AIRTHINGS_DEVICE_IDS"); v != "" {
		cfg.Airthings.DeviceIDs = splitCSV(v)
	}
	if v := os.Getenv("POLAR_SWITCHBOT_TOKEN"); v != "" {
		cfg.SwitchBot.Token = v
	}
	if v := os.Getenv("POLAR_SWITCHBOT_SECRET"); v != "" {
		cfg.SwitchBot.Secret = v
	}
	if v := os.Getenv("POLAR_NETATMO_CLIENT_ID"); v != "" {
		cfg.Netatmo.ClientID = v
	}
	if v := os.Getenv("POLAR_NETATMO_CLIENT_SECRET"); v != "" {
		cfg.Netatmo.ClientSecret = v
	}
	if v := os.Getenv("POLAR_NETATMO_REFRESH_TOKEN"); v != "" {
		cfg.Netatmo.RefreshToken = v
	}
	if v := os.Getenv("POLAR_DEFAULT_TARGET_ID"); v != "" {
		cfg.Monitoring.DefaultTargetID = v
	}
	if v := os.Getenv("POLAR_TARGETS_JSON"); v != "" {
		var targets []MonitorTargetConfig
		if err := json.Unmarshal([]byte(v), &targets); err == nil {
			cfg.Monitoring.Targets = targets
		}
	}
}

func validate(cfg Config) error {
	if cfg.Station.ID == "" {
		return errors.New("station.id is required")
	}
	if cfg.Polling.SensorInterval < time.Second {
		return errors.New("polling.sensor_interval must be >= 1s")
	}
	if cfg.Polling.ForecastInterval < time.Minute {
		return errors.New("polling.forecast_interval must be >= 1m")
	}
	if cfg.Polling.WeatherInterval < time.Minute {
		return errors.New("polling.weather_interval must be >= 1m")
	}
	if cfg.Polling.AirQualityInterval < time.Minute {
		return errors.New("polling.air_quality_interval must be >= 1m")
	}
	if cfg.Polling.AlertInterval < time.Minute {
		return errors.New("polling.alert_interval must be >= 1m")
	}
	if cfg.Auth.ServiceToken == "" && len(cfg.Auth.Tokens) == 0 {
		return errors.New("auth.service_token or auth.tokens is required")
	}
	validScopes := map[string]struct{}{
		"read:telemetry": {},
		"read:forecast":  {},
		"read:audit":     {},
		"admin:config":   {},
		"*":              {},
	}
	for i, token := range cfg.Auth.Tokens {
		if token.Name == "" {
			return errors.New("auth.tokens[" + strconv.Itoa(i) + "].name is required")
		}
		if token.Value == "" {
			return errors.New("auth.tokens[" + strconv.Itoa(i) + "].value is required")
		}
		if len(token.Scopes) == 0 {
			return errors.New("auth.tokens[" + strconv.Itoa(i) + "].scopes is required")
		}
		for _, scope := range token.Scopes {
			scope = strings.TrimSpace(scope)
			if _, ok := validScopes[scope]; !ok {
				return errors.New("auth.tokens[" + strconv.Itoa(i) + "].scopes contains unknown scope: " + scope)
			}
		}
	}
	if cfg.Provider.NOAABaseURL == "" {
		return errors.New("provider.noaa_base_url is required")
	}
	if cfg.Provider.AirNowURL == "" {
		return errors.New("provider.airnow_url is required")
	}
	targets := cfg.ConfiguredTargets()
	defaultFound := false
	for _, target := range targets {
		if target.ID == "" {
			return errors.New("monitoring.targets[].id is required")
		}
		if target.DisplayName == "" {
			return errors.New("monitoring.targets[].display_name is required")
		}
		if target.ID == cfg.DefaultTargetID() {
			defaultFound = true
		}
	}
	if !defaultFound {
		return errors.New("monitoring.default_target_id does not match a configured target")
	}
	return nil
}

func (cfg Config) DefaultTargetID() string {
	if cfg.Monitoring.DefaultTargetID != "" {
		return cfg.Monitoring.DefaultTargetID
	}
	targets := cfg.ConfiguredTargets()
	if len(targets) > 0 {
		return targets[0].ID
	}
	return cfg.Station.ID
}

func (cfg Config) ConfiguredTargets() []MonitorTargetConfig {
	return normalizedTargets(cfg)
}

func (cfg Config) MonitorTargets() []contracts.MonitorTarget {
	targets := cfg.ConfiguredTargets()
	out := make([]contracts.MonitorTarget, 0, len(targets))
	for _, target := range targets {
		out = append(out, contracts.MonitorTarget{
			ID:          target.ID,
			DisplayName: target.DisplayName,
			Latitude:    target.Latitude,
			Longitude:   target.Longitude,
			ZipCode:     target.ZipCode,
			Labels:      append([]string(nil), target.Labels...),
			Indoor:      target.IncludeIndoor,
			Weather:     target.EnableWeather,
			AirQuality:  target.EnableAirQuality,
			Default:     target.ID == cfg.DefaultTargetID(),
		})
	}
	return out
}

func (cfg Config) TargetByID(id string) (contracts.MonitorTarget, bool) {
	for _, target := range cfg.MonitorTargets() {
		if target.ID == id {
			return target, true
		}
	}
	return contracts.MonitorTarget{}, false
}

func normalizeMonitoring(cfg Config) MonitoringConfig {
	targets := normalizedTargets(cfg)
	defaultTargetID := cfg.Monitoring.DefaultTargetID
	if defaultTargetID == "" && len(targets) > 0 {
		defaultTargetID = targets[0].ID
	}
	return MonitoringConfig{
		DefaultTargetID: defaultTargetID,
		Targets:         targets,
	}
}

func normalizedTargets(cfg Config) []MonitorTargetConfig {
	if len(cfg.Monitoring.Targets) == 0 {
		return []MonitorTargetConfig{
			{
				ID:               cfg.Station.ID,
				DisplayName:      cfg.Station.ID,
				Latitude:         cfg.Station.Latitude,
				Longitude:        cfg.Station.Longitude,
				IncludeIndoor:    true,
				EnableWeather:    true,
				EnableAirQuality: true,
			},
		}
	}

	out := make([]MonitorTargetConfig, 0, len(cfg.Monitoring.Targets))
	defaultTarget := cfg.Monitoring.DefaultTargetID
	for i, target := range cfg.Monitoring.Targets {
		if target.DisplayName == "" {
			target.DisplayName = target.ID
		}
		if i == 0 && defaultTarget == "" {
			defaultTarget = target.ID
		}
		if i == 0 && !anyIndoor(cfg.Monitoring.Targets) {
			target.IncludeIndoor = true
		}
		if !target.EnableWeather && !target.EnableAirQuality && !target.IncludeIndoor {
			target.EnableWeather = true
			target.EnableAirQuality = true
		}
		out = append(out, target)
	}
	cfg.Monitoring.DefaultTargetID = defaultTarget
	return out
}

func anyIndoor(targets []MonitorTargetConfig) bool {
	for _, target := range targets {
		if target.IncludeIndoor {
			return true
		}
	}
	return false
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
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
