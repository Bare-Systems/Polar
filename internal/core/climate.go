package core

import (
	"fmt"
	"time"

	"polar/pkg/contracts"
)

type climateMetaEntry struct {
	displayName string
	domain      string
}

func climateMetricMeta(name string) climateMetaEntry {
	switch name {
	case "temperature":
		return climateMetaEntry{"Temperature", "thermal"}
	case "humidity":
		return climateMetaEntry{"Relative Humidity", "comfort"}
	case "pressure":
		return climateMetaEntry{"Pressure", "weather"}
	case "co2":
		return climateMetaEntry{"CO2", "air_quality"}
	case "voc":
		return climateMetaEntry{"VOCs", "air_quality"}
	case "radon":
		return climateMetaEntry{"Radon", "air_quality"}
	case "pm25":
		return climateMetaEntry{"PM2.5", "air_quality"}
	case "pm1":
		return climateMetaEntry{"PM1.0", "air_quality"}
	case "light":
		return climateMetaEntry{"Light Level", "comfort"}
	case "sound_level":
		return climateMetaEntry{"Sound Level", "comfort"}
	case "co":
		return climateMetaEntry{"Carbon Monoxide", "air_quality"}
	case "wind_speed":
		return climateMetaEntry{"Wind Speed", "weather"}
	case "precipitation":
		return climateMetaEntry{"Precipitation", "weather"}
	default:
		return climateMetaEntry{name, "other"}
	}
}

func normalizeReading(rd contracts.Reading) contracts.ClimateMetric {
	meta := climateMetricMeta(rd.Metric)
	return contracts.ClimateMetric{
		Name:         rd.Metric,
		DisplayName:  meta.displayName,
		Value:        rd.Value,
		Unit:         rd.Unit,
		DisplayValue: fmt.Sprintf("%.1f %s", rd.Value, rd.Unit),
		Domain:       meta.domain,
		Source:       rd.Source,
		Quality:      rd.QualityFlag,
		RecordedAt:   rd.RecordedAt,
	}
}

func outdoorMetricsFromPoint(pt contracts.ForecastPoint, source string) []contracts.ClimateMetric {
	at := pt.Time
	return []contracts.ClimateMetric{
		{
			Name: "temperature", DisplayName: "Temperature",
			Value: pt.TemperatureC, Unit: "°C", DisplayValue: fmt.Sprintf("%.1f °C", pt.TemperatureC),
			Domain: "thermal", Source: source, Quality: contracts.QualityEstimated, RecordedAt: at,
		},
		{
			Name: "humidity", DisplayName: "Relative Humidity",
			Value: pt.HumidityPct, Unit: "%", DisplayValue: fmt.Sprintf("%.0f%%", pt.HumidityPct),
			Domain: "comfort", Source: source, Quality: contracts.QualityEstimated, RecordedAt: at,
		},
		{
			Name: "wind_speed", DisplayName: "Wind Speed",
			Value: pt.WindSpeedMS, Unit: "m/s", DisplayValue: fmt.Sprintf("%.1f m/s", pt.WindSpeedMS),
			Domain: "weather", Source: source, Quality: contracts.QualityEstimated, RecordedAt: at,
		},
		{
			Name: "precipitation", DisplayName: "Precipitation",
			Value: pt.PrecipMM, Unit: "mm", DisplayValue: fmt.Sprintf("%.1f mm", pt.PrecipMM),
			Domain: "weather", Source: source, Quality: contracts.QualityEstimated, RecordedAt: at,
		},
	}
}

// closestForecastPoint returns the forecast point whose time is nearest to now.
func closestForecastPoint(points []contracts.ForecastPoint, now time.Time) *contracts.ForecastPoint {
	if len(points) == 0 {
		return nil
	}
	best := &points[0]
	bestDelta := absDuration(points[0].Time.Sub(now))
	for i := 1; i < len(points); i++ {
		d := absDuration(points[i].Time.Sub(now))
		if d < bestDelta {
			best = &points[i]
			bestDelta = d
		}
	}
	return best
}

// upcomingForecastPoints returns all forecast points within [now, now+window].
func upcomingForecastPoints(points []contracts.ForecastPoint, now time.Time, window time.Duration) []contracts.ForecastPoint {
	cutoff := now.Add(window)
	out := make([]contracts.ForecastPoint, 0)
	for _, pt := range points {
		if !pt.Time.Before(now) && !pt.Time.After(cutoff) {
			out = append(out, pt)
		}
	}
	return out
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
