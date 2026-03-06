package config

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Profile  string         `json:"profile"`
	Station  StationConfig  `json:"station"`
	Server   ServerConfig   `json:"server"`
	Storage  StorageConfig  `json:"storage"`
	Auth     AuthConfig     `json:"auth"`
	Features FeatureFlags   `json:"features"`
	Polling  PollingConfig  `json:"polling"`
	Provider ProviderConfig `json:"provider"`
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
	SQLitePath string `json:"sqlite_path"`
}

type AuthConfig struct {
	ServiceToken string `json:"service_token"`
}

type FeatureFlags struct {
	EnableForecast bool `json:"enable_forecast"`
	EnableMCP      bool `json:"enable_mcp"`
}

type PollingConfig struct {
	SensorInterval   time.Duration `json:"sensor_interval"`
	ForecastInterval time.Duration `json:"forecast_interval"`
}

type ProviderConfig struct {
	OpenMeteoURL string `json:"open_meteo_url"`
}

func defaults() Config {
	return Config{
		Profile:  "simulator",
		Station:  StationConfig{ID: "polar-dev", Latitude: 40.7128, Longitude: -74.0060},
		Server:   ServerConfig{ListenAddr: ":8080", MCPListenAddr: ":8081"},
		Storage:  StorageConfig{SQLitePath: "./polar.db"},
		Auth:     AuthConfig{ServiceToken: "dev-token"},
		Features: FeatureFlags{EnableForecast: true, EnableMCP: true},
		Polling:  PollingConfig{SensorInterval: 10 * time.Second, ForecastInterval: 30 * time.Minute},
		Provider: ProviderConfig{OpenMeteoURL: "https://api.open-meteo.com/v1/forecast"},
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
	if v := os.Getenv("POLAR_SQLITE_PATH"); v != "" {
		cfg.Storage.SQLitePath = v
	}
	if v := os.Getenv("POLAR_SERVICE_TOKEN"); v != "" {
		cfg.Auth.ServiceToken = v
	}
	if v := os.Getenv("POLAR_ENABLE_FORECAST"); v != "" {
		cfg.Features.EnableForecast = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("POLAR_ENABLE_MCP"); v != "" {
		cfg.Features.EnableMCP = strings.EqualFold(v, "true")
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
	if v := os.Getenv("POLAR_OPEN_METEO_URL"); v != "" {
		cfg.Provider.OpenMeteoURL = v
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
	if cfg.Auth.ServiceToken == "" {
		return errors.New("auth.service_token is required")
	}
	return nil
}
