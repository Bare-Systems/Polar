// purpleair.go fetches neighbourhood-level PM2.5/PM10 data from PurpleAir (C-6).
// API: https://api.purpleair.com/v1/sensors
package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"polar/pkg/contracts"
)

const purpleAirBase = "https://api.purpleair.com/v1/sensors"

// PurpleAirProvider fetches sensor readings from the PurpleAir API and
// averages PM2.5/PM10 values within radiusKm of the target.
type PurpleAirProvider struct {
	readKey  string
	radiusKm float64
	http     *http.Client
}

func NewPurpleAirProvider(readKey string, radiusKm float64, httpClient *http.Client) *PurpleAirProvider {
	if radiusKm <= 0 {
		radiusKm = 5
	}
	return &PurpleAirProvider{readKey: readKey, radiusKm: radiusKm, http: httpClient}
}

type purpleAirResp struct {
	Fields []string  `json:"fields"`
	Data   [][]any   `json:"data"`
}

// Fetch returns averaged PurpleAir PM2.5 and PM10 within the configured radius.
func (p *PurpleAirProvider) Fetch(ctx context.Context, target contracts.MonitorTarget) (contracts.PurpleAirAQ, error) {
	bbox := boundingBox(target.Latitude, target.Longitude, p.radiusKm)
	parts := splitAndTrim(bbox, ",")
	if len(parts) != 4 {
		return contracts.PurpleAirAQ{}, fmt.Errorf("purpleair: invalid bbox")
	}

	u, err := url.Parse(purpleAirBase)
	if err != nil {
		return contracts.PurpleAirAQ{}, err
	}
	q := u.Query()
	q.Set("fields", "latitude,longitude,pm2.5,pm10.0")
	q.Set("nwlng", parts[0])
	q.Set("selat", parts[1])
	q.Set("selng", parts[2])
	q.Set("nwlat", parts[3])
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return contracts.PurpleAirAQ{}, err
	}
	req.Header.Set("X-API-Key", p.readKey)

	resp, err := p.http.Do(req)
	if err != nil {
		return contracts.PurpleAirAQ{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return contracts.PurpleAirAQ{}, fmt.Errorf("purpleair status: %d", resp.StatusCode)
	}

	var payload purpleAirResp
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return contracts.PurpleAirAQ{}, err
	}

	latIdx, lonIdx, pm25Idx, pm10Idx := -1, -1, -1, -1
	for i, f := range payload.Fields {
		switch f {
		case "latitude":
			latIdx = i
		case "longitude":
			lonIdx = i
		case "pm2.5":
			pm25Idx = i
		case "pm10.0":
			pm10Idx = i
		}
	}
	if latIdx < 0 || lonIdx < 0 {
		return contracts.PurpleAirAQ{}, fmt.Errorf("purpleair: missing lat/lon fields")
	}

	var pm25Sum, pm10Sum float64
	count := 0
	for _, row := range payload.Data {
		if len(row) <= max3(latIdx, lonIdx, max3(pm25Idx, pm10Idx, 0)) {
			continue
		}
		lat, ok1 := toFloat(row[latIdx])
		lon, ok2 := toFloat(row[lonIdx])
		if !ok1 || !ok2 {
			continue
		}
		dist := haversineKm(target.Latitude, target.Longitude, lat, lon)
		if dist > p.radiusKm {
			continue
		}
		if pm25Idx >= 0 && pm25Idx < len(row) {
			if v, ok := toFloat(row[pm25Idx]); ok {
				pm25Sum += v
			}
		}
		if pm10Idx >= 0 && pm10Idx < len(row) {
			if v, ok := toFloat(row[pm10Idx]); ok {
				pm10Sum += v
			}
		}
		count++
	}

	pm25Avg := 0.0
	pm10Avg := 0.0
	if count > 0 {
		pm25Avg = pm25Sum / float64(count)
		pm10Avg = pm10Sum / float64(count)
	}

	return contracts.PurpleAirAQ{
		PM25Avg:     pm25Avg,
		PM10Avg:     pm10Avg,
		SensorCount: count,
		RadiusKm:    p.radiusKm,
		Source:      "purpleair",
		UpdatedAt:   time.Now().UTC(),
	}, nil
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	}
	return 0, false
}
