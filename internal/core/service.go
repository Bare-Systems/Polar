package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

	mu                   sync.RWMutex
	lastCollectorSuccess time.Time
	metrics              *obs.Metrics
	live                 *liveHub
}

func NewService(cfg config.Config, repo *storage.Repository, collectorSvc collector.Service, weatherClient providers.WeatherClient, fallbackClient providers.ForecastClient, airClient providers.AirQualityClient) *Service {
	return &Service{
		cfg:       cfg,
		repo:      repo,
		collector: collectorSvc,
		weather:   weatherClient,
		fallback:  fallbackClient,
		air:       airClient,
		metrics:   obs.NewMetrics(),
		live:      newLiveHub(),
	}
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

	_ = s.PullSensorReadings(ctx)
	_ = s.PullWeather(ctx)
	_ = s.PullAirQuality(ctx)
	_ = s.PullAlerts(ctx)

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
		}
	}
}

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
			s.publishSnapshot(ctx, target.ID)
		}
	}
	return nil
}

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
					s.publishSnapshot(ctx, target.ID)
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
	s.publishSnapshot(ctx, target.ID)
	return nil
}

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
	s.publishSnapshot(ctx, target.ID)
	return nil
}

func (s *Service) PullAlerts(ctx context.Context) error {
	return nil
}

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

func (s *Service) DataGaps(ctx context.Context) ([]contracts.DataGap, error) {
	latest, err := s.repo.LatestReadings(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	out := make([]contracts.DataGap, 0)
	for _, rd := range latest {
		lag := now.Sub(rd.RecordedAt)
		if lag > 2*s.cfg.Polling.SensorInterval {
			out = append(out, contracts.DataGap{Metric: rd.Metric, From: rd.RecordedAt, To: now, MissingForS: int(lag.Seconds())})
		}
	}
	return out, nil
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

	indoorSourceList := mapKeys(indoorSources)
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

	outdoor := contracts.OutdoorClimate{
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

func (s *Service) publishSnapshot(ctx context.Context, targetID string) {
	snap, err := s.ClimateSnapshotForTarget(ctx, targetID)
	if err != nil {
		return
	}
	s.live.Publish(contracts.LiveUpdate{
		Type:      "snapshot",
		TargetID:  targetID,
		Timestamp: time.Now().UTC(),
		Snapshot:  snap,
	})
}

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
