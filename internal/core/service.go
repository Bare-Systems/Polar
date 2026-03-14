package core

import (
	"context"
	"database/sql"
	"errors"
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
	forecast  providers.ForecastClient

	mu                      sync.RWMutex
	lastCollectorSuccess    time.Time
	lastForecastSuccess     time.Time
	lastForecastFailure     time.Time
	lastForecastError       string
	lastForecastFailureKind string
	metrics                 *obs.Metrics
}

func NewService(cfg config.Config, repo *storage.Repository, collectorSvc collector.Service, forecastClient providers.ForecastClient) *Service {
	return &Service{
		cfg:       cfg,
		repo:      repo,
		collector: collectorSvc,
		forecast:  forecastClient,
		metrics:   obs.NewMetrics(),
	}
}

func (s *Service) RunSchedulers(ctx context.Context) {
	sensorTicker := time.NewTicker(s.cfg.Polling.SensorInterval)
	defer sensorTicker.Stop()

	forecastTicker := time.NewTicker(s.cfg.Polling.ForecastInterval)
	defer forecastTicker.Stop()

	_ = s.PullSensorReadings(ctx)
	if s.cfg.Features.EnableForecast {
		_ = s.PullForecast(ctx)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-sensorTicker.C:
			_ = s.PullSensorReadings(context.Background())
		case <-forecastTicker.C:
			if s.cfg.Features.EnableForecast {
				_ = s.PullForecast(context.Background())
			}
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
	return nil
}

func (s *Service) PullForecast(ctx context.Context) error {
	startedAt := time.Now()
	snap, err := s.forecast.Fetch(ctx, s.cfg.Station.ID, s.cfg.Station.Latitude, s.cfg.Station.Longitude, s.cfg.Provider.OpenMeteoURL)
	if err != nil {
		s.recordForecastFailure("provider_error", err)
		s.metrics.RecordForecastRun(0, err, time.Since(startedAt))
		_ = s.repo.InsertAudit(ctx, "forecast.error", err.Error())
		return err
	}
	if err := s.repo.InsertForecast(ctx, snap); err != nil {
		s.recordForecastFailure("store_error", err)
		s.metrics.RecordForecastRun(len(snap.Points), err, time.Since(startedAt))
		_ = s.repo.InsertAudit(ctx, "forecast.store.error", err.Error())
		return err
	}
	s.mu.Lock()
	s.lastForecastSuccess = time.Now().UTC()
	s.lastForecastFailure = time.Time{}
	s.lastForecastError = ""
	s.lastForecastFailureKind = ""
	s.mu.Unlock()
	s.metrics.RecordForecastRun(len(snap.Points), nil, time.Since(startedAt))
	return nil
}

func (s *Service) Capabilities() contracts.CapabilityDescriptor {
	return contracts.CapabilityDescriptor{
		StationID:           s.cfg.Station.ID,
		SupportedMetrics:    []string{"temperature", "humidity", "light", "wind_speed", "pressure", "rainfall"},
		SupportsForecast:    s.cfg.Features.EnableForecast,
		SupportsCalibration: false,
		MinSamplingSeconds:  1,
		MaxSamplingSeconds:  3600,
	}
}

func (s *Service) StationHealth() contracts.StationHealth {
	now := time.Now().UTC()
	state := s.runtimeState()
	overall := "healthy"
	components := []contracts.ComponentHealth{
		{Name: "collector", Status: statusFor(state.lastCollectorSuccess, 2*s.cfg.Polling.SensorInterval), LastSuccess: state.lastCollectorSuccess, Message: "sensor sampling"},
		{Name: "storage", Status: "healthy", LastSuccess: now, Message: "sqlite ready"},
	}
	if s.cfg.Features.EnableForecast {
		forecastStatus := statusFor(state.lastForecastSuccess, 2*s.cfg.Polling.ForecastInterval)
		if state.lastForecastSuccess.IsZero() && !state.lastForecastFailure.IsZero() {
			forecastStatus = "degraded"
		}
		if state.lastForecastFailure.After(state.lastForecastSuccess) {
			forecastStatus = "degraded"
		}
		message := "open-meteo polling"
		if state.lastForecastError != "" {
			message = state.lastForecastError
		}
		components = append(components, contracts.ComponentHealth{
			Name: "forecast", Status: forecastStatus, LastSuccess: state.lastForecastSuccess, Message: message,
		})
	}
	for _, c := range components {
		if c.Status != "healthy" {
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
		if c.Name == "forecast" {
			continue
		}
		if c.Status == "starting" || c.Status == "degraded" {
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

func statusFor(last time.Time, maxLag time.Duration) string {
	if last.IsZero() {
		return "starting"
	}
	if time.Since(last) > maxLag {
		return "degraded"
	}
	return "healthy"
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
	now := time.Now().UTC()
	state := s.runtimeState()
	snap, err := s.repo.LatestForecast(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return contracts.ForecastSnapshot{
				StationID:   s.cfg.Station.ID,
				Provider:    "open-meteo",
				FetchedAt:   now,
				Stale:       true,
				StaleReason: s.staleReasonForMissingForecast(state),
				LastError:   state.lastForecastError,
			}, nil
		}
		return contracts.ForecastSnapshot{}, err
	}
	snap.FreshUntil = snap.FetchedAt.Add(2 * s.cfg.Polling.ForecastInterval)
	if state.lastForecastFailure.After(snap.FetchedAt) {
		snap.Stale = true
		snap.StaleReason = state.lastForecastFailureKind
		snap.LastError = state.lastForecastError
		return snap, nil
	}
	if now.After(snap.FreshUntil) {
		snap.Stale = true
		snap.StaleReason = "expired"
		snap.LastError = state.lastForecastError
		return snap, nil
	}
	snap.Stale = false
	snap.StaleReason = ""
	snap.LastError = ""
	return snap, nil
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
	return s.metrics.Snapshot(time.Now().UTC(), s.cfg.Polling.ForecastInterval)
}

func (s *Service) RecordRequestMetric(surface, name string, status int, duration time.Duration) {
	s.metrics.RecordRequest(surface, name, status, duration)
}

func (s *Service) RecordAuthFailure() {
	s.metrics.RecordAuthFailure()
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

type serviceState struct {
	lastCollectorSuccess    time.Time
	lastForecastSuccess     time.Time
	lastForecastFailure     time.Time
	lastForecastError       string
	lastForecastFailureKind string
}

func (s *Service) runtimeState() serviceState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return serviceState{
		lastCollectorSuccess:    s.lastCollectorSuccess,
		lastForecastSuccess:     s.lastForecastSuccess,
		lastForecastFailure:     s.lastForecastFailure,
		lastForecastError:       s.lastForecastError,
		lastForecastFailureKind: s.lastForecastFailureKind,
	}
}

func (s *Service) recordForecastFailure(kind string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastForecastFailure = time.Now().UTC()
	s.lastForecastError = err.Error()
	s.lastForecastFailureKind = kind
}

func (s *Service) staleReasonForMissingForecast(state serviceState) string {
	if state.lastForecastFailureKind != "" {
		return state.lastForecastFailureKind
	}
	return "unavailable"
}
