package core

import (
	"fmt"
	"sort"
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

func currentWeatherMetrics(current contracts.WeatherCurrent) []contracts.ClimateMetric {
	at := current.RecordedAt
	return []contracts.ClimateMetric{
		{
			Name: "temperature", DisplayName: "Temperature",
			Value: current.TemperatureC, Unit: "°C", DisplayValue: fmt.Sprintf("%.1f °C", current.TemperatureC),
			Domain: "thermal", Source: current.Source, Quality: current.Quality, RecordedAt: at,
		},
		{
			Name: "humidity", DisplayName: "Relative Humidity",
			Value: current.HumidityPct, Unit: "%", DisplayValue: fmt.Sprintf("%.0f%%", current.HumidityPct),
			Domain: "comfort", Source: current.Source, Quality: current.Quality, RecordedAt: at,
		},
		{
			Name: "wind_speed", DisplayName: "Wind Speed",
			Value: current.WindSpeedMS, Unit: "m/s", DisplayValue: fmt.Sprintf("%.1f m/s", current.WindSpeedMS),
			Domain: "weather", Source: current.Source, Quality: current.Quality, RecordedAt: at,
		},
		{
			Name: "pressure", DisplayName: "Pressure",
			Value: current.PressureHPa, Unit: "hPa", DisplayValue: fmt.Sprintf("%.1f hPa", current.PressureHPa),
			Domain: "weather", Source: current.Source, Quality: current.Quality, RecordedAt: at,
		},
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

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func aggregateSources(values ...string) []string {
	set := make(map[string]struct{})
	for _, value := range values {
		if value == "" {
			continue
		}
		set[value] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
