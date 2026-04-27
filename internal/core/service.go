package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"polar/internal/collector"
	"polar/internal/config"
	"polar/internal/obs"
	"polar/internal/providers"
	"polar/internal/storage"
	"polar/pkg/contracts"
)

type Service struct {
	cfg       config.Config
	repo      *storage.Repository
	collector collector.Service
	weather   providers.WeatherClient
	fallback  providers.ForecastClient
	air       providers.AirQualityClient

	// Phase C context providers (all optional).
	astronomy  *providers.AstronomyProvider
	wildfire   *providers.FIRMSProvider
	weatherAPI *providers.WeatherAPIClient
	purpleAir  *providers.PurpleAirProvider

	mu                   sync.RWMutex
	lastCollectorSuccess time.Time
	metrics              *obs.Metrics
	live                 *liveHub
}

func NewService(
	cfg config.Config,
	repo *storage.Repository,
	collectorSvc collector.Service,
	weatherClient providers.WeatherClient,
	fallbackClient providers.ForecastClient,
	airClient providers.AirQualityClient,
	opts ...ServiceOption,
) *Service {
	svc := &Service{
		cfg:       cfg,
		repo:      repo,
		collector: collectorSvc,
		weather:   weatherClient,
		fallback:  fallbackClient,
		air:       airClient,
		metrics:   obs.NewMetrics(),
		live:      newLiveHub(),
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// ServiceOption applies optional configuration to a Service.
type ServiceOption func(*Service)

func WithAstronomy(p *providers.AstronomyProvider) ServiceOption {
	return func(s *Service) { s.astronomy = p }
}

func WithWildfire(p *providers.FIRMSProvider) ServiceOption {
	return func(s *Service) { s.wildfire = p }
}

func WithWeatherAPI(p *providers.WeatherAPIClient) ServiceOption {
	return func(s *Service) { s.weatherAPI = p }
}

func WithPurpleAir(p *providers.PurpleAirProvider) ServiceOption {
	return func(s *Service) { s.purpleAir = p }
}

func (s *Service) RunSchedulers(ctx context.Context) {
	sensorTicker := time.NewTicker(s.cfg.Polling.SensorInterval)
	defer sensorTicker.Stop()

	weatherTicker := time.NewTicker(s.cfg.Polling.WeatherInterval)
	defer weatherTicker.Stop()

	airTicker := time.NewTicker(s.cfg.Polling.AirQualityInterval)
	defer airTicker.Stop()

	alertTicker := time.NewTicker(s.cfg.Polling.AlertInterval)
	defer alertTicker.Stop()

	wildfireTicker := time.NewTicker(s.cfg.Polling.WildfireInterval)
	defer wildfireTicker.Stop()

	pollenTicker := time.NewTicker(s.cfg.Polling.PollenInterval)
	defer pollenTicker.Stop()

	purpleAirTicker := time.NewTicker(s.cfg.Polling.PurpleAirInterval)
	defer purpleAirTicker.Stop()

	// Prune stale data nightly.
	pruneTicker := time.NewTicker(24 * time.Hour)
	defer pruneTicker.Stop()

	// Initial pulls on startup.
	_ = s.PullSensorReadings(ctx)
	_ = s.PullWeather(ctx)
	_ = s.PullAirQuality(ctx)
	_ = s.PullAlerts(ctx)
	s.pullAllContextProviders(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-sensorTicker.C:
			_ = s.PullSensorReadings(context.Background())
		case <-weatherTicker.C:
			_ = s.PullWeather(context.Background())
		case <-airTicker.C:
			_ = s.PullAirQuality(context.Background())
		case <-alertTicker.C:
			_ = s.PullAlerts(context.Background())
		case <-wildfireTicker.C:
			s.pullWildfire(context.Background())
		case <-pollenTicker.C:
			s.pullPollenAndUV(context.Background())
		case <-purpleAirTicker.C:
			s.pullPurpleAir(context.Background())
		case <-pruneTicker.C:
			s.prune(context.Background())
		}
	}
}

// --- Sensor collection ---

func (s *Service) PullSensorReadings(ctx context.Context) error {
	startedAt := time.Now()
	readings := s.collector.Collect()
	if len(readings) == 0 {
		s.metrics.RecordCollectorRun(0, nil, time.Since(startedAt))
		return nil
	}
	if err := s.repo.InsertReadings(ctx, readings); err != nil {
		s.metrics.RecordCollectorRun(len(readings), err, time.Since(startedAt))
		_ = s.repo.InsertAudit(ctx, "collector.error", err.Error())
		return err
	}
	s.mu.Lock()
	s.lastCollectorSuccess = time.Now().UTC()
	s.mu.Unlock()
	s.metrics.RecordCollectorRun(len(readings), nil, time.Since(startedAt))
	for _, target := range s.monitorTargets() {
		if target.Indoor {
			s.publishEvent(ctx, target.ID, contracts.LiveEventReadingUpdated)
		}
	}
	return nil
}

// --- Weather ---

func (s *Service) PullWeather(ctx context.Context) error {
	startedAt := time.Now()
	var firstErr error
	successful := 0
	for _, target := range s.monitorTargets() {
		if !target.Weather {
			continue
		}
		if err := s.pullWeatherForTarget(ctx, target); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			successful++
		}
	}
	s.metrics.RecordForecastRun(successful, firstErr, time.Since(startedAt))
	return firstErr
}

func (s *Service) pullWeatherForTarget(ctx context.Context, target contracts.MonitorTarget) error {
	if s.weather == nil {
		return nil
	}

	bundle, err := s.weather.Fetch(ctx, target)
	if err != nil {
		_ = s.recordProviderFailure(ctx, target.ID, "noaa", "weather", err, 2*s.cfg.Polling.WeatherInterval)
		_ = s.recordProviderFailure(ctx, target.ID, "noaa", "forecast", err, 2*s.cfg.Polling.ForecastInterval)
		_ = s.recordProviderFailure(ctx, target.ID, "noaa", "alerts", err, 2*s.cfg.Polling.AlertInterval)
		forecastErr := err
		if s.fallback != nil && s.cfg.Provider.OpenMeteoURL != "" {
			forecast, ferr := s.fallback.Fetch(ctx, target, s.cfg.Provider.OpenMeteoURL)
			if ferr == nil {
				forecast.TargetID = target.ID
				forecast.StationID = target.ID
				forecast.Stale = false
				forecast.StaleReason = ""
				forecast.LastError = ""
				if err := s.repo.InsertForecast(ctx, forecast); err == nil {
					_ = s.recordProviderSuccess(ctx, target.ID, "open-meteo", "forecast", 2*s.cfg.Polling.WeatherInterval)
					_ = s.repo.InsertAudit(ctx, "weather.fallback", fmt.Sprintf("target=%s provider=open-meteo reason=%v", target.ID, forecastErr))
					s.publishEvent(ctx, target.ID, contracts.LiveEventWeatherUpdated)
					return nil
				}
			}
		}
		_ = s.repo.InsertAudit(ctx, "weather.error", fmt.Sprintf("target=%s err=%v", target.ID, err))
		return err
	}

	if err := s.repo.StoreWeatherCurrent(ctx, bundle.Current); err != nil {
		return err
	}
	if err := s.repo.InsertForecast(ctx, bundle.Forecast); err != nil {
		return err
	}
	if err := s.repo.ReplaceAlerts(ctx, target.ID, "noaa", time.Now().UTC(), bundle.Alerts); err != nil {
		return err
	}
	_ = s.recordProviderSuccess(ctx, target.ID, "noaa", "weather", 2*s.cfg.Polling.WeatherInterval)
	_ = s.recordProviderSuccess(ctx, target.ID, "noaa", "forecast", 2*s.cfg.Polling.WeatherInterval)
	_ = s.recordProviderSuccess(ctx, target.ID, "noaa", "alerts", 2*s.cfg.Polling.AlertInterval)
	s.publishEvent(ctx, target.ID, contracts.LiveEventWeatherUpdated)
	return nil
}

// --- Air quality ---

func (s *Service) PullAirQuality(ctx context.Context) error {
	if s.air == nil {
		return nil
	}
	var firstErr error
	for _, target := range s.monitorTargets() {
		if !target.AirQuality {
			continue
		}
		if err := s.pullAirQualityForTarget(ctx, target); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Service) PullForecast(ctx context.Context) error {
	if s.fallback == nil {
		return nil
	}
	startedAt := time.Now()
	var firstErr error
	successful := 0
	for _, target := range s.monitorTargets() {
		if !target.Weather {
			continue
		}
		forecast, err := s.fallback.Fetch(ctx, target, s.cfg.Provider.OpenMeteoURL)
		if err != nil {
			_ = s.recordProviderFailure(ctx, target.ID, "open-meteo", "forecast", err, 2*s.cfg.Polling.ForecastInterval)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		forecast.TargetID = target.ID
		forecast.StationID = target.ID
		if err := s.repo.InsertForecast(ctx, forecast); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		_ = s.recordProviderSuccess(ctx, target.ID, forecast.Provider, "forecast", 2*s.cfg.Polling.ForecastInterval)
		successful++
	}
	s.metrics.RecordForecastRun(successful, firstErr, time.Since(startedAt))
	return firstErr
}

func (s *Service) pullAirQualityForTarget(ctx context.Context, target contracts.MonitorTarget) error {
	current, err := s.air.FetchCurrent(ctx, target)
	if err != nil {
		_ = s.recordProviderFailure(ctx, target.ID, "airnow", "air_quality_current", err, 2*s.cfg.Polling.AirQualityInterval)
		_ = s.repo.InsertAudit(ctx, "air_quality.current.error", fmt.Sprintf("target=%s err=%v", target.ID, err))
		return err
	}
	if err := s.repo.StoreAirQualityCurrent(ctx, current); err != nil {
		return err
	}
	forecast, err := s.air.FetchForecast(ctx, target)
	if err != nil {
		_ = s.recordProviderFailure(ctx, target.ID, "airnow", "air_quality_forecast", err, 2*s.cfg.Polling.AirQualityInterval)
		_ = s.repo.InsertAudit(ctx, "air_quality.forecast.error", fmt.Sprintf("target=%s err=%v", target.ID, err))
	} else {
		if err := s.repo.StoreAirQualityForecast(ctx, forecast); err != nil {
			return err
		}
		_ = s.recordProviderSuccess(ctx, target.ID, "airnow", "air_quality_forecast", 2*s.cfg.Polling.AirQualityInterval)
	}
	_ = s.recordProviderSuccess(ctx, target.ID, "airnow", "air_quality_current", 2*s.cfg.Polling.AirQualityInterval)
	s.publishEvent(ctx, target.ID, contracts.LiveEventAirQualityUpdated)
	return nil
}

func (s *Service) PullAlerts(ctx context.Context) error {
	return nil
}

// --- Phase C context providers ---

func (s *Service) pullAllContextProviders(ctx context.Context) {
	s.pullWildfire(ctx)
	s.pullPollenAndUV(ctx)
	s.pullPurpleAir(ctx)
}

func (s *Service) pullWildfire(ctx context.Context) {
	if s.wildfire == nil {
		return
	}
	for _, target := range s.monitorTargets() {
		wf, err := s.wildfire.Fetch(ctx, target)
		if err != nil {
			log.Printf("wildfire: target=%s err=%v", target.ID, err)
			_ = s.recordProviderFailure(ctx, target.ID, "nasa-firms", "wildfire", err, 2*s.cfg.Polling.WildfireInterval)
			continue
		}
		_ = s.repo.StoreContextSnapshot(ctx, target.ID, "wildfire", "nasa-firms", wf.UpdatedAt, wf)
		_ = s.recordProviderSuccess(ctx, target.ID, "nasa-firms", "wildfire", 2*s.cfg.Polling.WildfireInterval)
		s.publishEvent(ctx, target.ID, contracts.LiveEventSnapshotFull)
	}
}

func (s *Service) pullPollenAndUV(ctx context.Context) {
	if s.weatherAPI == nil {
		return
	}
	for _, target := range s.monitorTargets() {
		if s.cfg.Features.EnablePollen {
			pollen, err := s.weatherAPI.FetchPollen(ctx, target)
			if err != nil {
				log.Printf("pollen: target=%s err=%v", target.ID, err)
				_ = s.recordProviderFailure(ctx, target.ID, "weatherapi", "pollen", err, 2*s.cfg.Polling.PollenInterval)
			} else {
				_ = s.repo.StoreContextSnapshot(ctx, target.ID, "pollen", "weatherapi", pollen.UpdatedAt, pollen)
				_ = s.recordProviderSuccess(ctx, target.ID, "weatherapi", "pollen", 2*s.cfg.Polling.PollenInterval)
			}
		}
		if s.cfg.Features.EnableUV {
			uv, err := s.weatherAPI.FetchUV(ctx, target)
			if err != nil {
				log.Printf("uv: target=%s err=%v", target.ID, err)
				_ = s.recordProviderFailure(ctx, target.ID, "weatherapi", "uv", err, 2*s.cfg.Polling.PollenInterval)
			} else {
				_ = s.repo.StoreContextSnapshot(ctx, target.ID, "uv", "weatherapi", uv.UpdatedAt, uv)
				_ = s.recordProviderSuccess(ctx, target.ID, "weatherapi", "uv", 2*s.cfg.Polling.PollenInterval)
				s.publishEvent(ctx, target.ID, contracts.LiveEventSnapshotFull)
			}
		}
	}
}

func (s *Service) pullPurpleAir(ctx context.Context) {
	if s.purpleAir == nil {
		return
	}
	for _, target := range s.monitorTargets() {
		pa, err := s.purpleAir.Fetch(ctx, target)
		if err != nil {
			log.Printf("purpleair: target=%s err=%v", target.ID, err)
			_ = s.recordProviderFailure(ctx, target.ID, "purpleair", "air_quality", err, 2*s.cfg.Polling.PurpleAirInterval)
			continue
		}
		_ = s.repo.StoreContextSnapshot(ctx, target.ID, "purple_air", "purpleair", pa.UpdatedAt, pa)
		_ = s.recordProviderSuccess(ctx, target.ID, "purpleair", "air_quality", 2*s.cfg.Polling.PurpleAirInterval)
		s.publishEvent(ctx, target.ID, contracts.LiveEventAirQualityUpdated)
	}
}

// --- Pruning (A-1) ---

func (s *Service) prune(ctx context.Context) {
	now := time.Now().UTC()

	if days := s.cfg.Retention.OutdoorSnapshotDays; days > 0 {
		before := now.AddDate(0, 0, -days)
		n, err := s.repo.PruneOutdoorSnapshots(ctx, before)
		if err != nil {
			log.Printf("prune: outdoor_snapshots error: %v", err)
		} else if n > 0 {
			log.Printf("prune: removed %d outdoor_snapshots older than %d days", n, days)
		}
		n2, err := s.repo.PruneLegacyForecasts(ctx, before)
		if err != nil {
			log.Printf("prune: forecasts error: %v", err)
		} else if n2 > 0 {
			log.Printf("prune: removed %d legacy forecast rows older than %d days", n2, days)
		}
	}

	if days := s.cfg.Retention.AlertDays; days > 0 {
		before := now.AddDate(0, 0, -days)
		n, err := s.repo.PruneAlerts(ctx, before)
		if err != nil {
			log.Printf("prune: weather_alerts error: %v", err)
		} else if n > 0 {
			log.Printf("prune: removed %d weather_alerts older than %d days", n, days)
		}
	}

	if days := s.cfg.Retention.AuditDays; days > 0 {
		before := now.AddDate(0, 0, -days)
		n, err := s.repo.PruneAuditEvents(ctx, before)
		if err != nil {
			log.Printf("prune: audit_events error: %v", err)
		} else if n > 0 {
			log.Printf("prune: removed %d audit_events older than %d days", n, days)
		}
	}
}

// --- Source license seeding (A-5) ---

// SeedSourceLicenses upserts the known-good license metadata for all built-in
// providers. Call once after migration at startup.
func (s *Service) SeedSourceLicenses(ctx context.Context) {
	licenses := []contracts.SourceLicense{
		{
			Provider:            "noaa",
			License:             "U.S. Government Open Data",
			AttributionRequired: false,
			LicenseURL:          "https://www.weather.gov/disclaimer",
			RetentionDays:       0,
			Notes:               "Official U.S. NWS weather, forecast, and alert data. No attribution required for U.S. government works.",
		},
		{
			Provider:            "open-meteo",
			License:             "CC BY 4.0",
			AttributionRequired: true,
			LicenseURL:          "https://creativecommons.org/licenses/by/4.0/",
			RetentionDays:       0,
			Notes:               "Free tier requires CC BY 4.0 attribution. Commercial use requires a paid license.",
		},
		{
			Provider:            "airnow",
			License:             "U.S. EPA Open Data",
			AttributionRequired: false,
			LicenseURL:          "https://www.airnowapi.org/aq/observation/zipCode/current/",
			RetentionDays:       0,
			Notes:               "U.S. EPA AirNow AQI data. Open government data.",
		},
		{
			Provider:            "nasa-firms",
			License:             "NASA Open Data",
			AttributionRequired: true,
			LicenseURL:          "https://firms.modaps.eosdis.nasa.gov/",
			RetentionDays:       1,
			Notes:               "Near-real-time fire data. Citation: NASA FIRMS VIIRS Active Fire data.",
		},
		{
			Provider:            "weatherapi",
			License:             "Proprietary (WeatherAPI.com)",
			AttributionRequired: false,
			LicenseURL:          "https://www.weatherapi.com/terms.aspx",
			RetentionDays:       0,
			Notes:               "Commercial API. Pollen and UV data. Attribution not required but data must not be redistributed.",
		},
		{
			Provider:            "purpleair",
			License:             "Proprietary (PurpleAir)",
			AttributionRequired: true,
			LicenseURL:          "https://api.purpleair.com/",
			RetentionDays:       7,
			Notes:               "Points-based billing. Attribution required when displaying sensor data.",
		},
		{
			Provider:            "airthings",
			License:             "Proprietary (Airthings)",
			AttributionRequired: false,
			LicenseURL:          "https://developer.airthings.com/",
			RetentionDays:       0,
			Notes:               "Indoor air quality from user-owned Airthings devices.",
		},
		{
			Provider:            "shelly",
			License:             "Local Device (no license)",
			AttributionRequired: false,
			LicenseURL:          "",
			RetentionDays:       0,
			Notes:               "LAN-only Shelly device data. No external service or license.",
		},
		{
			Provider:            "switchbot",
			License:             "Proprietary (SwitchBot)",
			AttributionRequired: false,
			LicenseURL:          "https://github.com/OpenWonderLabs/SwitchBotAPI",
			RetentionDays:       0,
			Notes:               "SwitchBot OpenAPI. Personal use; commercial use requires agreement with SwitchBot.",
		},
	}
	for _, lic := range licenses {
		if err := s.repo.UpsertSourceLicense(ctx, lic); err != nil {
			log.Printf("seed licenses: %s: %v", lic.Provider, err)
		}
	}
}

// SourceLicenses returns all registered provider license records.
func (s *Service) SourceLicenses(ctx context.Context) ([]contracts.SourceLicense, error) {
	return s.repo.ListSourceLicenses(ctx)
}

// SeedConsentGrants persists a row for each configured active integration (X-4).
// This is called once at startup so the consent table always reflects the current
// provider configuration without requiring manual SQL.
func (s *Service) SeedConsentGrants(ctx context.Context) {
	grants := s.activeConsentGrants()
	for _, g := range grants {
		if err := s.repo.UpsertConsentGrant(ctx, g); err != nil {
			log.Printf("seed consent grants: %s/%s: %v", g.TargetID, g.Provider, err)
		}
	}
}

// ConsentGrants returns all consent grants recorded in the database (X-4).
func (s *Service) ConsentGrants(ctx context.Context) ([]contracts.ConsentGrant, error) {
	return s.repo.ListConsentGrants(ctx)
}

// activeConsentGrants builds the set of ConsentGrants that reflect the currently
// enabled integrations in the service configuration.
func (s *Service) activeConsentGrants() []contracts.ConsentGrant {
	now := time.Now().UTC()
	var grants []contracts.ConsentGrant

	add := func(targetID, provider, accountSubject string, scopes, classes []string, retentionDays int, shareAgents, shareDash bool, licenseReq string) {
		grants = append(grants, contracts.ConsentGrant{
			ID:                  provider + ":" + targetID,
			TargetID:            targetID,
			Provider:            provider,
			AccountSubject:      accountSubject,
			GrantedScopes:       scopes,
			DataClasses:         classes,
			RetentionDays:       retentionDays,
			ShareWithAgents:     shareAgents,
			ShareWithDashboards: shareDash,
			LicenseRequirements: licenseReq,
			GrantedAt:           now,
		})
	}

	// Station-wide outdoor data providers.
	defaultTarget := s.cfg.DefaultTargetID()
	for _, t := range s.monitorTargets() {
		tid := t.ID
		if t.Weather {
			add(tid, "noaa", "", []string{"weather:read"}, []string{"weather", "forecast", "alerts"}, 0, true, true, "U.S. Government Open Data")
			add(tid, "open-meteo", "", []string{"forecast:read"}, []string{"forecast"}, 0, true, true, "CC BY 4.0")
		}
		if t.AirQuality {
			add(tid, "airnow", "", []string{"air_quality:read"}, []string{"air_quality"}, 0, true, true, "U.S. EPA Open Data")
		}
	}

	// Indoor sensor integrations (station-wide).
	cfg := s.cfg
	if len(cfg.Shelly.Devices) > 0 {
		for _, dev := range cfg.Shelly.Devices {
			add(defaultTarget, "shelly", dev.IP, []string{"telemetry:read"}, []string{"temperature", "humidity"}, 0, true, true, "Local Device")
		}
	}
	if cfg.SwitchBot.Token != "" {
		for _, dev := range cfg.SwitchBot.Devices {
			add(defaultTarget, "switchbot", dev.DeviceID, []string{"telemetry:read"}, []string{"temperature", "humidity"}, 0, true, true, "SwitchBot OpenAPI")
		}
	}
	if cfg.Airthings.ClientID != "" {
		for _, devID := range cfg.Airthings.DeviceIDs {
			add(defaultTarget, "airthings", devID, []string{"telemetry:read"}, []string{"temperature", "humidity", "co2", "voc", "radon"}, 0, true, true, "Proprietary (Airthings)")
		}
	}
	if cfg.Netatmo.ClientID != "" {
		for _, devID := range cfg.Netatmo.DeviceIDs {
			add(defaultTarget, "netatmo", devID, []string{"telemetry:read"}, []string{"temperature", "humidity", "co2", "pressure", "noise"}, 0, true, true, "Proprietary (Netatmo)")
		}
	}

	// Optional Phase C context providers.
	if cfg.Provider.FIRMSAPIKey != "" {
		add(defaultTarget, "nasa-firms", "", []string{"wildfire:read"}, []string{"wildfire"}, 1, true, true, "NASA Open Data")
	}
	if cfg.Provider.WeatherAPIKey != "" {
		add(defaultTarget, "weatherapi", "", []string{"pollen:read", "uv:read"}, []string{"pollen", "uv"}, 0, true, true, "Proprietary (WeatherAPI.com)")
	}
	if cfg.Provider.PurpleAirAPIKey != "" {
		add(defaultTarget, "purpleair", "", []string{"air_quality:read"}, []string{"pm25", "pm10"}, 7, true, true, "Proprietary (PurpleAir)")
	}

	return grants
}

// --- Read methods ---

func (s *Service) Capabilities() contracts.CapabilityDescriptor {
	metrics := []string{"temperature", "humidity", "light", "wind_speed", "pressure", "rainfall"}
	type metricsProvider interface {
		Metrics() []string
	}
	if mp, ok := s.collector.(metricsProvider); ok {
		metrics = mp.Metrics()
	}
	targets := s.monitorTargets()
	support := make([]contracts.ProviderTarget, 0, len(targets))
	for _, target := range targets {
		support = append(support, contracts.ProviderTarget{
			TargetID:   target.ID,
			Weather:    target.Weather,
			AirQuality: target.AirQuality,
			Indoor:     target.Indoor,
		})
	}
	return contracts.CapabilityDescriptor{
		StationID:           s.cfg.Station.ID,
		SupportedMetrics:    metrics,
		SupportsForecast:    true,
		SupportsCalibration: false,
		MinSamplingSeconds:  1,
		MaxSamplingSeconds:  3600,
		Targets:             targets,
		ProviderSupport:     support,
	}
}

func (s *Service) StationHealth() contracts.StationHealth {
	now := time.Now().UTC()
	s.mu.RLock()
	lastCollector := s.lastCollectorSuccess
	s.mu.RUnlock()

	components := []contracts.ComponentHealth{
		{Name: "collector", Status: statusFor(lastCollector, 2*s.cfg.Polling.SensorInterval), LastSuccess: lastCollector, Message: "sensor sampling"},
		{Name: "storage", Status: "healthy", LastSuccess: now, Message: storageMessage(s.cfg.Storage)},
	}

	defaultTargetID := s.cfg.DefaultTargetID()
	if statuses, err := s.repo.ProviderStatuses(context.Background(), defaultTargetID); err == nil {
		for _, status := range statuses {
			lastSuccess := time.Time{}
			if status.LastSuccessAt != nil {
				lastSuccess = *status.LastSuccessAt
			}
			message := status.Provider
			if status.LastError != "" {
				message = status.LastError
			}
			components = append(components, contracts.ComponentHealth{
				Name:        status.Provider + ":" + status.Component,
				Status:      status.Status,
				LastSuccess: lastSuccess,
				Message:     message,
			})
		}
	}

	overall := "healthy"
	for _, component := range components {
		if component.Status != "healthy" {
			overall = "degraded"
			break
		}
	}

	// SLO breaches also degrade overall health (X-3).
	if overall == "healthy" {
		if breaches := s.checkSLOBreaches(context.Background(), now); len(breaches) > 0 {
			overall = "degraded"
			for _, b := range breaches {
				components = append(components, contracts.ComponentHealth{
					Name:    "slo:" + b.TargetID + ":" + b.Component,
					Status:  "degraded",
					Message: fmt.Sprintf("data age %ds exceeds SLO %ds", b.ActualAgeS, b.MaxAgeS),
				})
			}
		}
	}

	return contracts.StationHealth{StationID: s.cfg.Station.ID, Overall: overall, Components: components, GeneratedAt: now}
}

func (s *Service) Readiness() contracts.ReadinessStatus {
	health := s.StationHealth()
	ready := true
	for _, c := range health.Components {
		if c.Name == "collector" && c.Status != "healthy" {
			ready = false
			break
		}
	}
	status := "ready"
	if !ready {
		status = "not_ready"
	}
	return contracts.ReadinessStatus{
		StationID:   health.StationID,
		Ready:       ready,
		Status:      status,
		Components:  health.Components,
		GeneratedAt: health.GeneratedAt,
	}
}

func (s *Service) LatestReadings(ctx context.Context) ([]contracts.Reading, error) {
	return s.repo.LatestReadings(ctx)
}

func (s *Service) QueryReadings(ctx context.Context, metric string, from, to time.Time) ([]contracts.Reading, error) {
	return s.repo.QueryReadings(ctx, metric, from, to)
}

func (s *Service) QueryReadingsAtResolution(ctx context.Context, metric string, from, to time.Time, resolution time.Duration) ([]contracts.Reading, error) {
	readings, err := s.repo.QueryReadings(ctx, metric, from, to)
	if err != nil || resolution <= 0 {
		return readings, err
	}
	return aggregateReadings(readings, resolution), nil
}

func (s *Service) LatestForecast(ctx context.Context) (contracts.ForecastSnapshot, error) {
	return s.ForecastForTarget(ctx, s.cfg.DefaultTargetID())
}

func (s *Service) ForecastForTarget(ctx context.Context, targetID string) (contracts.ForecastSnapshot, error) {
	target, err := s.resolveTarget(targetID)
	if err != nil {
		return contracts.ForecastSnapshot{}, err
	}
	snap, err := s.repo.LatestForecastForTarget(ctx, target.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return contracts.ForecastSnapshot{
				TargetID:    target.ID,
				StationID:   s.cfg.Station.ID,
				Provider:    "noaa",
				Latitude:    target.Latitude,
				Longitude:   target.Longitude,
				FetchedAt:   time.Now().UTC(),
				Stale:       true,
				StaleReason: "unavailable",
			}, nil
		}
		return contracts.ForecastSnapshot{}, err
	}
	snap.FreshUntil = snap.FetchedAt.Add(2 * s.cfg.Polling.ForecastInterval)
	if time.Now().UTC().After(snap.FreshUntil) {
		snap.Stale = true
		snap.StaleReason = "expired"
	}
	statuses, _ := s.repo.ProviderStatuses(ctx, target.ID)
	applyForecastStatus(&snap, statuses)
	return snap, nil
}

func (s *Service) WeatherCurrent(ctx context.Context, targetID string) (contracts.WeatherCurrent, error) {
	target, err := s.resolveTarget(targetID)
	if err != nil {
		return contracts.WeatherCurrent{}, err
	}
	current, err := s.repo.LatestWeatherCurrent(ctx, target.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return contracts.WeatherCurrent{
				TargetID:    target.ID,
				Source:      "noaa",
				FetchedAt:   time.Now().UTC(),
				RecordedAt:  time.Now().UTC(),
				Quality:     contracts.QualityUnavailable,
				Stale:       true,
				StaleReason: "unavailable",
			}, nil
		}
		return contracts.WeatherCurrent{}, err
	}
	if status := providerStatusByComponent(mustStatuses(s.repo.ProviderStatuses(ctx, target.ID)), "weather"); status != nil {
		current.Stale = status.Stale
		current.StaleReason = staleReasonForStatus(*status)
		current.LastError = status.LastError
	}
	return current, nil
}

func (s *Service) AirQualityCurrent(ctx context.Context, targetID string) (contracts.AirQualityCurrent, error) {
	target, err := s.resolveTarget(targetID)
	if err != nil {
		return contracts.AirQualityCurrent{}, err
	}
	current, err := s.repo.LatestAirQualityCurrent(ctx, target.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return contracts.AirQualityCurrent{
				TargetID:    target.ID,
				Source:      "airnow",
				FetchedAt:   time.Now().UTC(),
				RecordedAt:  time.Now().UTC(),
				Stale:       true,
				StaleReason: "unavailable",
			}, nil
		}
		return contracts.AirQualityCurrent{}, err
	}
	if status := providerStatusByComponent(mustStatuses(s.repo.ProviderStatuses(ctx, target.ID)), "air_quality_current"); status != nil {
		current.Stale = status.Stale
		current.StaleReason = staleReasonForStatus(*status)
		current.LastError = status.LastError
	}
	return current, nil
}

func (s *Service) AirQualityForecast(ctx context.Context, targetID string) (contracts.AirQualityForecast, error) {
	target, err := s.resolveTarget(targetID)
	if err != nil {
		return contracts.AirQualityForecast{}, err
	}
	forecast, err := s.repo.LatestAirQualityForecast(ctx, target.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return contracts.AirQualityForecast{
				TargetID:    target.ID,
				Source:      "airnow",
				FetchedAt:   time.Now().UTC(),
				Stale:       true,
				StaleReason: "unavailable",
			}, nil
		}
		return contracts.AirQualityForecast{}, err
	}
	if status := providerStatusByComponent(mustStatuses(s.repo.ProviderStatuses(ctx, target.ID)), "air_quality_forecast"); status != nil {
		forecast.Stale = status.Stale
		forecast.StaleReason = staleReasonForStatus(*status)
		forecast.LastError = status.LastError
	}
	return forecast, nil
}

func (s *Service) WeatherAlerts(ctx context.Context, targetID string) ([]contracts.WeatherAlert, error) {
	target, err := s.resolveTarget(targetID)
	if err != nil {
		return nil, err
	}
	return s.repo.ActiveAlerts(ctx, target.ID)
}

func (s *Service) DataGaps(ctx context.Context) (contracts.DiagnosticsReport, error) {
	latest, err := s.repo.LatestReadings(ctx)
	if err != nil {
		return contracts.DiagnosticsReport{}, err
	}
	now := time.Now().UTC()

	gaps := make([]contracts.DataGap, 0)
	for _, rd := range latest {
		lag := now.Sub(rd.RecordedAt)
		if lag > 2*s.cfg.Polling.SensorInterval {
			gaps = append(gaps, contracts.DataGap{
				Metric:      rd.Metric,
				From:        rd.RecordedAt,
				To:          now,
				MissingForS: int(lag.Seconds()),
			})
		}
	}

	breaches := s.checkSLOBreaches(ctx, now)

	return contracts.DiagnosticsReport{
		DataGaps:    gaps,
		SLOBreaches: breaches,
		GeneratedAt: now,
	}, nil
}

// checkSLOBreaches evaluates per-target freshness SLOs for indoor, weather,
// and air quality data. It records a metric counter for each breach found (X-3).
func (s *Service) checkSLOBreaches(ctx context.Context, now time.Time) []contracts.SLOBreach {
	slo := s.cfg.SLO
	var breaches []contracts.SLOBreach

	for _, target := range s.monitorTargets() {
		// Indoor: keyed off the collector's last success timestamp.
		if slo.IndoorMaxAgeS > 0 && target.Indoor {
			s.mu.RLock()
			lastIndoor := s.lastCollectorSuccess
			s.mu.RUnlock()
			age := int(now.Sub(lastIndoor).Seconds())
			if !lastIndoor.IsZero() && age > slo.IndoorMaxAgeS {
				s.metrics.RecordSLOBreach()
				t := lastIndoor
				breaches = append(breaches, contracts.SLOBreach{
					TargetID:   target.ID,
					Component:  "indoor",
					MaxAgeS:    slo.IndoorMaxAgeS,
					ActualAgeS: age,
					LastSeenAt: &t,
				})
			}
		}

		// Weather: keyed off the provider status for "weather" component.
		if slo.WeatherMaxAgeS > 0 && target.Weather {
			statuses, _ := s.repo.ProviderStatuses(ctx, target.ID)
			if ps := providerStatusByComponent(statuses, "weather"); ps != nil && ps.LastSuccessAt != nil {
				age := int(now.Sub(*ps.LastSuccessAt).Seconds())
				if age > slo.WeatherMaxAgeS {
					s.metrics.RecordSLOBreach()
					t := *ps.LastSuccessAt
					breaches = append(breaches, contracts.SLOBreach{
						TargetID:   target.ID,
						Component:  "weather",
						MaxAgeS:    slo.WeatherMaxAgeS,
						ActualAgeS: age,
						LastSeenAt: &t,
					})
				}
			}
		}

		// Air quality: keyed off "air_quality_current" provider status.
		if slo.AQMaxAgeS > 0 && target.AirQuality {
			statuses, _ := s.repo.ProviderStatuses(ctx, target.ID)
			if ps := providerStatusByComponent(statuses, "air_quality_current"); ps != nil && ps.LastSuccessAt != nil {
				age := int(now.Sub(*ps.LastSuccessAt).Seconds())
				if age > slo.AQMaxAgeS {
					s.metrics.RecordSLOBreach()
					t := *ps.LastSuccessAt
					breaches = append(breaches, contracts.SLOBreach{
						TargetID:   target.ID,
						Component:  "air_quality",
						MaxAgeS:    slo.AQMaxAgeS,
						ActualAgeS: age,
						LastSeenAt: &t,
					})
				}
			}
		}
	}
	return breaches
}

// --- Command plane (D-1) ---

// terminalStatus returns true for states that may not be transitioned further.
func terminalStatus(s contracts.CommandStatus) bool {
	switch s {
	case contracts.CommandStatusSucceeded,
		contracts.CommandStatusFailed,
		contracts.CommandStatusExpired,
		contracts.CommandStatusRejected:
		return true
	}
	return false
}

// SubmitCommand validates and persists a new command with status "pending".
// If cmd.IdempotencyKey is non-empty and a command with the same key already
// exists for the target, the existing command is returned without creating a
// duplicate (idempotent submit).
func (s *Service) SubmitCommand(ctx context.Context, cmd contracts.Command) (contracts.Command, error) {
	if cmd.Capability == "" {
		return contracts.Command{}, fmt.Errorf("capability is required")
	}
	if _, err := s.resolveTarget(cmd.TargetID); err != nil {
		return contracts.Command{}, fmt.Errorf("unknown target %q: %w", cmd.TargetID, err)
	}
	if cmd.ExpiresAt != nil && cmd.ExpiresAt.Before(time.Now().UTC()) {
		return contracts.Command{}, fmt.Errorf("expires_at is already in the past")
	}

	// Idempotency check: if a key is provided, return any existing command for
	// the same (target, idempotency_key) pair instead of creating a duplicate.
	if cmd.IdempotencyKey != "" {
		if existing, err := s.repo.FindCommandByIdempotencyKey(ctx, cmd.TargetID, cmd.IdempotencyKey); err == nil {
			return existing, nil
		}
	}

	cmd.Status = contracts.CommandStatusPending
	cmd.RequestedAt = time.Now().UTC()
	if err := s.repo.InsertCommand(ctx, cmd); err != nil {
		return contracts.Command{}, err
	}
	return cmd, nil
}

// UpdateCommandStatus transitions an existing command to a new status, writing a
// corresponding CommandResult row. Terminal states (succeeded, failed, expired,
// rejected) cannot be transitioned further.
func (s *Service) UpdateCommandStatus(ctx context.Context, commandID string, newStatus contracts.CommandStatus, observedEffect, errMsg string) (contracts.Command, contracts.CommandResult, error) {
	cmd, err := s.repo.GetCommand(ctx, commandID)
	if err != nil {
		return contracts.Command{}, contracts.CommandResult{}, err
	}
	if terminalStatus(cmd.Status) {
		return contracts.Command{}, contracts.CommandResult{}, fmt.Errorf("command %s is already in terminal state %q", commandID, cmd.Status)
	}

	now := time.Now().UTC()

	// Build the result record, preserving any previously written accepted_at.
	result := contracts.CommandResult{
		CommandID:      commandID,
		Status:         newStatus,
		ObservedEffect: observedEffect,
		FinalStatus:    newStatus,
		Error:          errMsg,
		UpdatedAt:      now,
	}
	if newStatus == contracts.CommandStatusAccepted {
		result.AcceptedAt = &now
	}
	if newStatus == contracts.CommandStatusExecuting || terminalStatus(newStatus) {
		acked := now
		result.ProviderAcknowledgedAt = &acked
	}

	// Merge with any existing result (so we don't overwrite accepted_at).
	if existing, err := s.repo.GetCommandResult(ctx, commandID); err == nil {
		if existing.AcceptedAt != nil && result.AcceptedAt == nil {
			result.AcceptedAt = existing.AcceptedAt
		}
		if existing.ProviderAcknowledgedAt != nil && result.ProviderAcknowledgedAt == nil {
			result.ProviderAcknowledgedAt = existing.ProviderAcknowledgedAt
		}
	}

	if err := s.repo.UpdateCommandStatus(ctx, commandID, newStatus); err != nil {
		return contracts.Command{}, contracts.CommandResult{}, err
	}
	if err := s.repo.UpsertCommandResult(ctx, result); err != nil {
		return contracts.Command{}, contracts.CommandResult{}, err
	}

	cmd.Status = newStatus
	return cmd, result, nil
}

// GetCommand returns the command and its result (if any) for the given ID.
func (s *Service) GetCommand(ctx context.Context, commandID string) (contracts.Command, *contracts.CommandResult, error) {
	cmd, err := s.repo.GetCommand(ctx, commandID)
	if err != nil {
		return contracts.Command{}, nil, err
	}
	result, err := s.repo.GetCommandResult(ctx, commandID)
	if err != nil {
		// No result yet is normal.
		return cmd, nil, nil
	}
	return cmd, &result, nil
}

// ListCommands returns the most recent commands for a target.
func (s *Service) ListCommands(ctx context.Context, targetID string, limit int) ([]contracts.Command, error) {
	if _, err := s.resolveTarget(targetID); err != nil {
		return nil, fmt.Errorf("unknown target %q: %w", targetID, err)
	}
	return s.repo.ListCommandsForTarget(ctx, targetID, limit)
}

func (s *Service) AuditEvents(ctx context.Context, from, to time.Time, eventType string) ([]map[string]any, error) {
	return s.repo.QueryAudit(ctx, from, to, eventType)
}

func (s *Service) MetricsSnapshot() obs.Snapshot {
	return s.metrics.Snapshot(time.Now().UTC(), s.cfg.Polling.WeatherInterval)
}

func (s *Service) RecordRequestMetric(surface, name string, status int, duration time.Duration) {
	s.metrics.RecordRequest(surface, name, status, duration)
}

func (s *Service) RecordAuthFailure() {
	s.metrics.RecordAuthFailure()
}

func (s *Service) Targets() []contracts.MonitorTarget {
	return s.monitorTargets()
}

func (s *Service) ClimateSnapshot(ctx context.Context) (contracts.ClimateSnapshot, error) {
	return s.ClimateSnapshotForTarget(ctx, s.cfg.DefaultTargetID())
}

func (s *Service) ClimateSnapshotForTarget(ctx context.Context, targetID string) (contracts.ClimateSnapshot, error) {
	target, err := s.resolveTarget(targetID)
	if err != nil {
		return contracts.ClimateSnapshot{}, err
	}

	now := time.Now().UTC()
	rawReadings, err := s.repo.LatestReadings(ctx)
	if err != nil {
		return contracts.ClimateSnapshot{}, err
	}

	var latestReadingAt *time.Time
	indoorReadings := make([]contracts.ClimateMetric, 0, len(rawReadings))
	indoorSources := make(map[string]struct{})
	if target.Indoor {
		for _, rd := range rawReadings {
			indoorReadings = append(indoorReadings, normalizeReading(rd))
			indoorSources[rd.Source] = struct{}{}
			if latestReadingAt == nil || rd.RecordedAt.After(*latestReadingAt) {
				t := rd.RecordedAt
				latestReadingAt = &t
			}
		}
	}

	weatherCurrent, _ := s.WeatherCurrent(ctx, target.ID)
	forecast, _ := s.ForecastForTarget(ctx, target.ID)
	airCurrent, _ := s.AirQualityCurrent(ctx, target.ID)
	airForecast, _ := s.AirQualityForecast(ctx, target.ID)
	alerts, _ := s.WeatherAlerts(ctx, target.ID)
	statuses, _ := s.repo.ProviderStatuses(ctx, target.ID)

	// Build enrichment context (Phase C — silently ignore missing data).
	outdoor := s.buildOutdoorClimate(ctx, target, weatherCurrent, forecast, airCurrent, airForecast, alerts, statuses)

	indoorSourceList := mapKeys(indoorSources)

	return contracts.ClimateSnapshot{
		StationID:   s.cfg.Station.ID,
		TargetID:    target.ID,
		GeneratedAt: now,
		Indoor: contracts.IndoorClimate{
			Sources:       indoorSourceList,
			Readings:      indoorReadings,
			LastReadingAt: latestReadingAt,
			Stale:         target.Indoor && (latestReadingAt == nil || now.Sub(*latestReadingAt) > 2*s.cfg.Polling.SensorInterval),
		},
		Outdoor: outdoor,
	}, nil
}

func (s *Service) buildOutdoorClimate(
	ctx context.Context,
	target contracts.MonitorTarget,
	weatherCurrent contracts.WeatherCurrent,
	forecast contracts.ForecastSnapshot,
	airCurrent contracts.AirQualityCurrent,
	airForecast contracts.AirQualityForecast,
	alerts []contracts.WeatherAlert,
	statuses []contracts.ProviderStatus,
) contracts.OutdoorClimate {
	outdoorSources := aggregateSources(weatherCurrent.Source, forecast.Provider, airCurrent.Source, airForecast.Source)
	var lastFetched *time.Time
	var freshUntil *time.Time
	if !forecast.FetchedAt.IsZero() {
		t := forecast.FetchedAt
		lastFetched = &t
	}
	if !forecast.FreshUntil.IsZero() {
		t := forecast.FreshUntil
		freshUntil = &t
	}

	oc := contracts.OutdoorClimate{
		Target:          target,
		Sources:         outdoorSources,
		CurrentWeather:  &weatherCurrent,
		Forecast:        &forecast,
		AirQuality:      &airCurrent,
		AirQualityTrend: &airForecast,
		Alerts:          alerts,
		Statuses:        statuses,
		Current:         currentWeatherMetrics(weatherCurrent),
		LastFetchedAt:   lastFetched,
		FreshUntil:      freshUntil,
		Stale:           weatherCurrent.Stale || forecast.Stale || airCurrent.Stale || airForecast.Stale,
	}

	// Astronomy (C-4): always available when enabled, computed in-process.
	if s.astronomy != nil && s.cfg.Features.EnableAstronomy {
		astro := s.astronomy.Compute(ctx, target)
		oc.Astronomy = &astro
	}

	// Wildfire (C-1).
	if s.cfg.Features.EnableWildfire {
		if wf, err := s.repo.LatestWildfireContext(ctx, target.ID); err == nil {
			oc.Wildfire = &wf
		}
	}

	// Pollen (C-2).
	if s.cfg.Features.EnablePollen {
		if pollen, err := s.repo.LatestPollenContext(ctx, target.ID); err == nil {
			oc.Pollen = &pollen
		}
	}

	// UV (C-3).
	if s.cfg.Features.EnableUV {
		if uv, err := s.repo.LatestUVContext(ctx, target.ID); err == nil {
			oc.UV = &uv
		}
	}

	// PurpleAir (C-6).
	if s.cfg.Features.EnablePurpleAir {
		if pa, err := s.repo.LatestPurpleAirAQ(ctx, target.ID); err == nil {
			oc.PurpleAir = &pa
		}
	}

	return oc
}

// WildfireContext returns the latest wildfire context for a target.
func (s *Service) WildfireContext(ctx context.Context, targetID string) (contracts.WildfireContext, error) {
	target, err := s.resolveTarget(targetID)
	if err != nil {
		return contracts.WildfireContext{}, err
	}
	return s.repo.LatestWildfireContext(ctx, target.ID)
}

// PollenContext returns the latest pollen context for a target.
func (s *Service) PollenContext(ctx context.Context, targetID string) (contracts.PollenContext, error) {
	target, err := s.resolveTarget(targetID)
	if err != nil {
		return contracts.PollenContext{}, err
	}
	return s.repo.LatestPollenContext(ctx, target.ID)
}

// UVContext returns the latest UV context for a target.
func (s *Service) UVContext(ctx context.Context, targetID string) (contracts.UVContext, error) {
	target, err := s.resolveTarget(targetID)
	if err != nil {
		return contracts.UVContext{}, err
	}
	return s.repo.LatestUVContext(ctx, target.ID)
}

// PurpleAirAQ returns the latest PurpleAir AQ reading for a target.
func (s *Service) PurpleAirAQ(ctx context.Context, targetID string) (contracts.PurpleAirAQ, error) {
	target, err := s.resolveTarget(targetID)
	if err != nil {
		return contracts.PurpleAirAQ{}, err
	}
	return s.repo.LatestPurpleAirAQ(ctx, target.ID)
}

// AstronomyContext returns the astronomy context for a target.
func (s *Service) AstronomyContext(ctx context.Context, targetID string) (contracts.AstronomyContext, error) {
	target, err := s.resolveTarget(targetID)
	if err != nil {
		return contracts.AstronomyContext{}, err
	}
	if s.astronomy != nil {
		astro := s.astronomy.Compute(ctx, target)
		return astro, nil
	}
	return s.repo.LatestAstronomyContext(ctx, target.ID)
}

// --- Live feed ---

func (s *Service) Subscribe(targetID string) (chan contracts.LiveUpdate, error) {
	target, err := s.resolveTarget(targetID)
	if err != nil {
		return nil, err
	}
	return s.live.Subscribe(target.ID), nil
}

func (s *Service) Unsubscribe(targetID string, ch chan contracts.LiveUpdate) {
	s.live.Unsubscribe(targetID, ch)
}

// publishEvent builds a full snapshot and publishes it with the given event type.
// This lets callers distinguish between reading_updated, weather_updated, etc.
func (s *Service) publishEvent(ctx context.Context, targetID, eventType string) {
	snap, err := s.ClimateSnapshotForTarget(ctx, targetID)
	if err != nil {
		return
	}
	s.live.Publish(contracts.LiveUpdate{
		Type:      eventType,
		TargetID:  targetID,
		Timestamp: time.Now().UTC(),
		Snapshot:  snap,
	})
}

// --- Provider status helpers ---

func (s *Service) recordProviderSuccess(ctx context.Context, targetID, provider, component string, freshness time.Duration) error {
	now := time.Now().UTC()
	freshUntil := now.Add(freshness)
	return s.repo.UpsertProviderStatus(ctx, contracts.ProviderStatus{
		TargetID:      targetID,
		Provider:      provider,
		Component:     component,
		Status:        "healthy",
		LastSuccessAt: &now,
		FreshUntil:    &freshUntil,
		Stale:         false,
	})
}

func (s *Service) recordProviderFailure(ctx context.Context, targetID, provider, component string, err error, freshness time.Duration) error {
	statuses, _ := s.repo.ProviderStatuses(ctx, targetID)
	var lastSuccess *time.Time
	if existing := providerStatus(statuses, provider, component); existing != nil {
		lastSuccess = existing.LastSuccessAt
	}
	now := time.Now().UTC()
	freshUntil := now.Add(freshness)
	return s.repo.UpsertProviderStatus(ctx, contracts.ProviderStatus{
		TargetID:      targetID,
		Provider:      provider,
		Component:     component,
		Status:        "degraded",
		LastSuccessAt: lastSuccess,
		LastFailureAt: &now,
		LastError:     err.Error(),
		FreshUntil:    &freshUntil,
		Stale:         true,
	})
}

func (s *Service) monitorTargets() []contracts.MonitorTarget {
	return s.cfg.MonitorTargets()
}

func (s *Service) resolveTarget(id string) (contracts.MonitorTarget, error) {
	if id == "" {
		id = s.cfg.DefaultTargetID()
	}
	target, ok := s.cfg.TargetByID(id)
	if !ok {
		return contracts.MonitorTarget{}, fmt.Errorf("unknown target: %s", id)
	}
	return target, nil
}

// --- Aggregation helpers ---

func aggregateReadings(readings []contracts.Reading, resolution time.Duration) []contracts.Reading {
	if len(readings) == 0 {
		return readings
	}
	type key struct {
		sensorID string
		bucket   time.Time
	}
	out := make([]contracts.Reading, 0, len(readings))
	counts := make([]int, 0, len(readings))
	idxByKey := make(map[key]int, len(readings))
	for _, rd := range readings {
		bucket := rd.RecordedAt.UTC().Truncate(resolution)
		k := key{sensorID: rd.SensorID, bucket: bucket}
		idx, ok := idxByKey[k]
		if !ok {
			aggregated := rd
			aggregated.RecordedAt = bucket
			aggregated.QualityFlag = contracts.QualityEstimated
			out = append(out, aggregated)
			counts = append(counts, 1)
			idxByKey[k] = len(out) - 1
			continue
		}
		out[idx].Value += rd.Value
		counts[idx]++
		if rd.ReceivedAt.After(out[idx].ReceivedAt) {
			out[idx].ReceivedAt = rd.ReceivedAt
		}
	}
	for i := range out {
		out[i].Value /= float64(counts[i])
	}
	return out
}

func mapKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func storageMessage(cfg config.StorageConfig) string {
	if cfg.Driver == storage.DialectPostgres || cfg.DatabaseURL != "" {
		return "postgres ready"
	}
	return "sqlite ready"
}

func applyForecastStatus(snap *contracts.ForecastSnapshot, statuses []contracts.ProviderStatus) {
	status := providerStatusByComponent(statuses, "forecast")
	if status == nil {
		return
	}
	snap.Stale = status.Stale
	snap.LastError = status.LastError
	snap.StaleReason = staleReasonForStatus(*status)
	if status.FreshUntil != nil {
		snap.FreshUntil = *status.FreshUntil
	}
}

func providerStatus(statuses []contracts.ProviderStatus, provider, component string) *contracts.ProviderStatus {
	for _, status := range statuses {
		if status.Provider == provider && status.Component == component {
			copyStatus := status
			return &copyStatus
		}
	}
	return nil
}

func providerStatusByComponent(statuses []contracts.ProviderStatus, component string) *contracts.ProviderStatus {
	for _, status := range statuses {
		if status.Component == component {
			copyStatus := status
			return &copyStatus
		}
	}
	return nil
}

func staleReasonForStatus(status contracts.ProviderStatus) string {
	if status.LastError != "" {
		return "provider_error"
	}
	if status.Stale {
		return "expired"
	}
	return ""
}

func mustStatuses(statuses []contracts.ProviderStatus, err error) []contracts.ProviderStatus {
	if err != nil {
		return nil
	}
	return statuses
}
