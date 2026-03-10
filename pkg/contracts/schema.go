package contracts

// SchemaVersion is the current v1alpha1 contract version emitted in all
// API and MCP responses via the X-Polar-Schema HTTP header.
// Bump this when the contract set is promoted (e.g. v1beta1, v1).
const SchemaVersion = "v1alpha1"

// Metric names supported by the baseline sensor set.
const (
	MetricTemperature = "temperature"
	MetricHumidity    = "humidity"
	MetricLight       = "light"
	MetricWindSpeed   = "wind_speed"
	MetricPressure    = "pressure"
	MetricRainfall    = "rainfall"
)

// metricUnits maps each supported metric to its canonical SI unit string.
var metricUnits = map[string]string{
	MetricTemperature: "C",
	MetricHumidity:    "%",
	MetricLight:       "lux",
	MetricWindSpeed:   "m/s",
	MetricPressure:    "hPa",
	MetricRainfall:    "mm",
}

// UnitFor returns the canonical unit for a metric name, or an empty string
// if the metric is not in the baseline catalog.
func UnitFor(metric string) string {
	return metricUnits[metric]
}

// SupportedMetrics returns the ordered list of baseline metric names.
func SupportedMetrics() []string {
	return []string{
		MetricTemperature,
		MetricHumidity,
		MetricLight,
		MetricWindSpeed,
		MetricPressure,
		MetricRainfall,
	}
}
