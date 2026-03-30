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
	Provider   ProviderConfig   `json:"provider"`
	Airthings  AirthingsConfig  `json:"airthings"`
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

type TokenConfig struct {
	Name   string   `json:"name"`
	Value  string   `json:"value"`
	Scopes []string `json:"scopes"`
}

type FeatureFlags struct {
	EnableForecast  bool `json:"enable_forecast"`
	EnableMCP       bool `json:"enable_mcp"`
	EnableAirthings bool `json:"enable_airthings"`
	EnableLive      bool `json:"enable_live"`
}

type PollingConfig struct {
	SensorInterval     time.Duration `json:"sensor_interval"`
	ForecastInterval   time.Duration `json:"forecast_interval"`
	WeatherInterval    time.Duration `json:"weather_interval"`
	AirQualityInterval time.Duration `json:"air_quality_interval"`
	AlertInterval      time.Duration `json:"alert_interval"`
}

type ProviderConfig struct {
	OpenMeteoURL  string `json:"open_meteo_url"`
	NOAABaseURL   string `json:"noaa_base_url"`
	NOAAUserAgent string `json:"noaa_user_agent"`
	AirNowURL     string `json:"airnow_url"`
	AirNowToken   string `json:"airnow_token"`
}

type AirthingsConfig struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
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
		Features: FeatureFlags{EnableForecast: true, EnableMCP: true, EnableLive: true},
		Polling: PollingConfig{
			SensorInterval:     10 * time.Second,
			ForecastInterval:   30 * time.Minute,
			WeatherInterval:    15 * time.Minute,
			AirQualityInterval: 30 * time.Minute,
			AlertInterval:      5 * time.Minute,
		},
		Provider: ProviderConfig{
			OpenMeteoURL:  "https://api.open-meteo.com/v1/forecast",
			NOAABaseURL:   "https://api.weather.gov",
			NOAAUserAgent: "Polar/0.2 (ops@baresystems.local)",
			AirNowURL:     "https://www.airnowapi.org/aq",
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
	if v := os.Getenv("POLAR_AIRTHINGS_CLIENT_ID"); v != "" {
		cfg.Airthings.ClientID = v
	}
	if v := os.Getenv("POLAR_AIRTHINGS_CLIENT_SECRET"); v != "" {
		cfg.Airthings.ClientSecret = v
	}
	if v := os.Getenv("POLAR_AIRTHINGS_DEVICE_IDS"); v != "" {
		cfg.Airthings.DeviceIDs = splitCSV(v)
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
