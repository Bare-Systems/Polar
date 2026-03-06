package collector

import (
	"math"
	"math/rand"
	"time"

	"polar/internal/config"
	"polar/pkg/contracts"
)

type Service interface {
	Collect() []contracts.Reading
}

type SimulatorService struct {
	cfg config.Config
	rng *rand.Rand
}

func NewSimulatorService(cfg config.Config) *SimulatorService {
	return &SimulatorService{
		cfg: cfg,
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *SimulatorService) Collect() []contracts.Reading {
	now := time.Now().UTC()
	t := float64(now.Unix()%86400) / 86400.0 * 2 * math.Pi
	mk := func(sensorID, metric, unit string, base, amp, noise float64) contracts.Reading {
		v := base + amp*math.Sin(t) + (s.rng.Float64()*2-1)*noise
		return contracts.Reading{
			StationID:   s.cfg.Station.ID,
			SensorID:    sensorID,
			Metric:      metric,
			Value:       v,
			Unit:        unit,
			Source:      "simulator",
			QualityFlag: contracts.QualityGood,
			RecordedAt:  now,
			ReceivedAt:  now,
		}
	}
	return []contracts.Reading{
		mk("sim-temp", "temperature", "C", 17, 8, 0.7),
		mk("sim-humidity", "humidity", "%RH", 58, 20, 2.5),
		mk("sim-light", "light", "lux", 400, 350, 25),
		mk("sim-wind", "wind_speed", "m/s", 3, 2, 0.6),
		mk("sim-pressure", "pressure", "hPa", 1015, 6, 0.8),
		mk("sim-rain", "rainfall", "mm", 0.2, 0.2, 0.05),
	}
}
