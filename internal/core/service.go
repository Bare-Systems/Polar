package core

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"polar/internal/collector"
	"polar/internal/config"
	"polar/internal/providers"
	"polar/internal/storage"
	"polar/pkg/contracts"
)

type Service struct {
	cfg       config.Config
	repo      *storage.Repository
	collector collector.Service
	forecast  providers.ForecastClient

	lastCollectorSuccess time.Time
	lastForecastSuccess  time.Time
}

func NewService(cfg config.Config, repo *storage.Repository, collectorSvc collector.Service, forecastClient providers.ForecastClient) *Service {
	return &Service{cfg: cfg, repo: repo, collector: collectorSvc, forecast: forecastClient}
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
	readings := s.collector.Collect()
	if len(readings) == 0 {
		return nil
	}
	if err := s.repo.InsertReadings(ctx, readings); err != nil {
		_ = s.repo.InsertAudit(ctx, "collector.error", err.Error())
		return err
	}
	s.lastCollectorSuccess = time.Now().UTC()
	return nil
}

func (s *Service) PullForecast(ctx context.Context) error {
	snap, err := s.forecast.Fetch(ctx, s.cfg.Station.ID, s.cfg.Station.Latitude, s.cfg.Station.Longitude, s.cfg.Provider.OpenMeteoURL)
	if err != nil {
		_ = s.repo.InsertAudit(ctx, "forecast.error", err.Error())
		return err
	}
	if err := s.repo.InsertForecast(ctx, snap); err != nil {
		_ = s.repo.InsertAudit(ctx, "forecast.store.error", err.Error())
		return err
	}
	s.lastForecastSuccess = time.Now().UTC()
	return nil
}

func (s *Service) Capabilities() contracts.CapabilityDescriptor {
	return contracts.CapabilityDescriptor{
		StationID:           s.cfg.Station.ID,
		SupportedMetrics:    contracts.SupportedMetrics(),
		SupportsForecast:    s.cfg.Features.EnableForecast,
		SupportsCalibration: false,
		MinSamplingSeconds:  1,
		MaxSamplingSeconds:  3600,
	}
}

func (s *Service) StationHealth() contracts.StationHealth {
	now := time.Now().UTC()
	overall := "healthy"
	components := []contracts.ComponentHealth{
		{Name: "collector", Status: statusFor(s.lastCollectorSuccess, 2*s.cfg.Polling.SensorInterval), LastSuccess: s.lastCollectorSuccess, Message: "sensor sampling"},
		{Name: "storage", Status: "healthy", LastSuccess: now, Message: "sqlite ready"},
	}
	if s.cfg.Features.EnableForecast {
		components = append(components, contracts.ComponentHealth{
			Name: "forecast", Status: statusFor(s.lastForecastSuccess, 2*s.cfg.Polling.ForecastInterval), LastSuccess: s.lastForecastSuccess, Message: "open-meteo polling",
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
	snap, err := s.repo.LatestForecast(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return contracts.ForecastSnapshot{StationID: s.cfg.Station.ID, Provider: "open-meteo", FetchedAt: time.Now().UTC(), Stale: true}, nil
		}
		return contracts.ForecastSnapshot{}, err
	}
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
