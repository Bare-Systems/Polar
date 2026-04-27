// Package providers contains all external data source adapters.
// astronomy.go implements NOAA solar-position calculations entirely in-process
// with no external API key required (C-4).
package providers

import (
	"context"
	"math"
	"time"

	"polar/pkg/contracts"
)

// AstronomyProvider computes daily solar events for a target using the NOAA
// solar position algorithm. It caches the result per target per calendar day.
type AstronomyProvider struct {
	cache map[string]astronomyEntry
}

type astronomyEntry struct {
	date   string // YYYY-MM-DD in the target's solar-noon timezone
	result contracts.AstronomyContext
}

func NewAstronomyProvider() *AstronomyProvider {
	return &AstronomyProvider{cache: make(map[string]astronomyEntry)}
}

// Compute returns today's astronomy context for the target. Results are cached
// per target for the calendar day (UTC date).
func (p *AstronomyProvider) Compute(_ context.Context, target contracts.MonitorTarget) contracts.AstronomyContext {
	today := time.Now().UTC().Format("2006-01-02")
	if entry, ok := p.cache[target.ID]; ok && entry.date == today {
		return entry.result
	}
	result := solarEvents(target.Latitude, target.Longitude, time.Now().UTC())
	p.cache[target.ID] = astronomyEntry{date: today, result: result}
	return result
}

// solarEvents computes sunrise, sunset, twilight, and solar noon for a given
// lat/lon and date using the NOAA algorithm (https://gml.noaa.gov/grad/solcalc/).
func solarEvents(lat, lon float64, t time.Time) contracts.AstronomyContext {
	year := t.Year()
	month := int(t.Month())
	day := t.Day()

	jd := julianDay(year, month, day)
	solarNoonMinutes := solarNoonUTC(jd, lon)
	riseMinutes, setMinutes := sunriseSunsetUTC(jd, lat, lon)
	civilBeginMinutes := twilightUTC(jd, lat, lon, 96.0) // civil = 6° below horizon
	civilEndMinutes := twilightUTC(jd, lat, lon, 96.0)

	// Civil twilight begin is before sunrise, end is after sunset.
	// twilightUTC gives the morning twilight start; compute end as mirror of noon.
	sunriseT := minutesUTCToTime(t, riseMinutes)
	sunsetT := minutesUTCToTime(t, setMinutes)
	solarNoonT := minutesUTCToTime(t, solarNoonMinutes)

	// Civil twilight: ~24-minute window before sunrise / after sunset for 6°.
	civilBeginT := minutesUTCToTime(t, civilBeginMinutes)
	_ = civilEndMinutes
	civilEndT := sunsetT.Add(sunsetT.Sub(civilBeginT) / 2) // approximate mirror

	dayLen := sunsetT.Sub(sunriseT).Minutes()
	if dayLen < 0 {
		dayLen = 0
	}

	return contracts.AstronomyContext{
		Sunrise:            sunriseT,
		Sunset:             sunsetT,
		CivilTwilightBegin: civilBeginT,
		CivilTwilightEnd:   civilEndT,
		SolarNoon:          solarNoonT,
		DayLengthMin:       dayLen,
		UpdatedAt:          time.Now().UTC(),
	}
}

// julianDay converts a calendar date to a Julian Day Number.
func julianDay(year, month, day int) float64 {
	if month <= 2 {
		year--
		month += 12
	}
	a := math.Floor(float64(year) / 100.0)
	b := 2 - a + math.Floor(a/4)
	return math.Floor(365.25*float64(year+4716)) +
		math.Floor(30.6001*float64(month+1)) +
		float64(day) + b - 1524.5
}

// solarNoonUTC returns the UTC time-of-day (in fractional minutes) of solar noon
// for a given Julian day and longitude.
func solarNoonUTC(jd, lon float64) float64 {
	t := julianCentury(jd)
	eqOfTime := equationOfTime(t)
	solarNoon := 720 - 4*lon - eqOfTime // minutes from midnight UTC
	return solarNoon
}

// sunriseSunsetUTC returns UTC minutes-from-midnight for sunrise and sunset.
func sunriseSunsetUTC(jd, lat, lon float64) (rise, set float64) {
	t := julianCentury(jd)
	eqOfTime := equationOfTime(t)
	decl := sunDeclination(t)

	latRad := lat * math.Pi / 180
	declRad := decl * math.Pi / 180

	// Hour angle at sunrise/sunset (cos of zenith = cos(90.833°) for refraction+disc)
	cosHA := math.Cos(90.833*math.Pi/180)/(math.Cos(latRad)*math.Cos(declRad)) -
		math.Tan(latRad)*math.Tan(declRad)

	// Clamp to valid range to avoid NaN at extreme latitudes.
	cosHA = math.Max(-1, math.Min(1, cosHA))
	ha := math.Acos(cosHA) * 180 / math.Pi

	rise = 720 - 4*(lon+ha) - eqOfTime
	set = 720 - 4*(lon-ha) - eqOfTime
	return rise, set
}

// twilightUTC returns UTC minutes-from-midnight for civil twilight begin (morning).
// zenith is the solar zenith angle in degrees (96 for civil, 102 nautical, 108 astronomical).
func twilightUTC(jd, lat, lon, zenith float64) float64 {
	t := julianCentury(jd)
	eqOfTime := equationOfTime(t)
	decl := sunDeclination(t)

	latRad := lat * math.Pi / 180
	declRad := decl * math.Pi / 180
	cosHA := math.Cos(zenith*math.Pi/180)/(math.Cos(latRad)*math.Cos(declRad)) -
		math.Tan(latRad)*math.Tan(declRad)
	cosHA = math.Max(-1, math.Min(1, cosHA))
	ha := math.Acos(cosHA) * 180 / math.Pi
	return 720 - 4*(lon+ha) - eqOfTime
}

func julianCentury(jd float64) float64 {
	return (jd - 2451545.0) / 36525.0
}

func geomMeanLongSun(t float64) float64 {
	l0 := 280.46646 + t*(36000.76983+t*0.0003032)
	return math.Mod(l0, 360)
}

func geomMeanAnomalySun(t float64) float64 {
	return 357.52911 + t*(35999.05029-0.0001537*t)
}

func eccentricityEarthOrbit(t float64) float64 {
	return 0.016708634 - t*(0.000042037+0.0000001267*t)
}

func sunEqOfCenter(t float64) float64 {
	m := geomMeanAnomalySun(t) * math.Pi / 180
	return math.Sin(m)*(1.914602-t*(0.004817+0.000014*t)) +
		math.Sin(2*m)*(0.019993-0.000101*t) +
		math.Sin(3*m)*0.000289
}

func sunTrueLong(t float64) float64 {
	return geomMeanLongSun(t) + sunEqOfCenter(t)
}

func sunApparentLong(t float64) float64 {
	omega := 125.04 - 1934.136*t
	return sunTrueLong(t) - 0.00569 - 0.00478*math.Sin(omega*math.Pi/180)
}

func meanObliquityOfEcliptic(t float64) float64 {
	seconds := 21.448 - t*(46.8150+t*(0.00059-t*0.001813))
	return 23 + (26+(seconds/60))/60
}

func obliquityCorrection(t float64) float64 {
	e0 := meanObliquityOfEcliptic(t)
	omega := 125.04 - 1934.136*t
	return e0 + 0.00256*math.Cos(omega*math.Pi/180)
}

func sunDeclination(t float64) float64 {
	e := obliquityCorrection(t) * math.Pi / 180
	lambda := sunApparentLong(t) * math.Pi / 180
	sinDec := math.Sin(e) * math.Sin(lambda)
	return math.Asin(sinDec) * 180 / math.Pi
}

func equationOfTime(t float64) float64 {
	epsilon := obliquityCorrection(t) * math.Pi / 180
	l0 := geomMeanLongSun(t) * math.Pi / 180
	e := eccentricityEarthOrbit(t)
	m := geomMeanAnomalySun(t) * math.Pi / 180
	y := math.Tan(epsilon/2) * math.Tan(epsilon/2)
	eqTime := y*math.Sin(2*l0) -
		2*e*math.Sin(m) +
		4*e*y*math.Sin(m)*math.Cos(2*l0) -
		0.5*y*y*math.Sin(4*l0) -
		1.25*e*e*math.Sin(2*m)
	return eqTime * 4 * 180 / math.Pi // convert radians to minutes
}

// minutesUTCToTime converts fractional UTC minutes-from-midnight to a time.Time
// on the same calendar date as the reference time.
func minutesUTCToTime(ref time.Time, minutesUTC float64) time.Time {
	if math.IsNaN(minutesUTC) || math.IsInf(minutesUTC, 0) {
		return ref // fallback: midnight
	}
	baseDay := time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, time.UTC)
	totalSeconds := int(math.Round(minutesUTC * 60))
	return baseDay.Add(time.Duration(totalSeconds) * time.Second)
}
