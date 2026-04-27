// firms.go fetches active fire hotspot data from NASA FIRMS (C-1).
// API: https://firms.modaps.eosdis.nasa.gov/api/area/csv/<MAP_KEY>/VIIRS_SNPP_NRT/<bbox>/1
package providers

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"polar/pkg/contracts"
)

// FIRMSProvider fetches NASA FIRMS wildfire hotspot data.
type FIRMSProvider struct {
	apiKey   string
	radiusKm float64
	http     *http.Client
}

func NewFIRMSProvider(apiKey string, radiusKm float64, httpClient *http.Client) *FIRMSProvider {
	if radiusKm <= 0 {
		radiusKm = 50
	}
	return &FIRMSProvider{apiKey: apiKey, radiusKm: radiusKm, http: httpClient}
}

// Fetch retrieves FIRMS hotspots within a bounding box around the target and
// returns a WildfireContext describing proximity and risk level.
func (p *FIRMSProvider) Fetch(ctx context.Context, target contracts.MonitorTarget) (contracts.WildfireContext, error) {
	bbox := boundingBox(target.Latitude, target.Longitude, p.radiusKm)
	reqURL := fmt.Sprintf(
		"https://firms.modaps.eosdis.nasa.gov/api/area/csv/%s/VIIRS_SNPP_NRT/%s/1",
		p.apiKey,
		bbox,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return contracts.WildfireContext{}, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return contracts.WildfireContext{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return contracts.WildfireContext{}, fmt.Errorf("firms status: %d", resp.StatusCode)
	}

	hotspots, err := parseFIRMSCSV(resp.Body, target.Latitude, target.Longitude, p.radiusKm)
	if err != nil {
		return contracts.WildfireContext{}, err
	}

	nearest := math.MaxFloat64
	for _, dist := range hotspots {
		if dist < nearest {
			nearest = dist
		}
	}
	if nearest == math.MaxFloat64 {
		nearest = 0
	}

	count := len(hotspots)
	risk := wildfireRisk(nearest, count)

	return contracts.WildfireContext{
		NearestHotspotKm:       nearest,
		ActiveHotspotsInRadius: count,
		RiskLevel:              risk,
		RadiusKm:               p.radiusKm,
		Source:                 "nasa-firms",
		UpdatedAt:              time.Now().UTC(),
	}, nil
}

// parseFIRMSCSV parses the FIRMS CSV response and returns a slice of distances
// (km) from the target for each hotspot within radiusKm.
func parseFIRMSCSV(r io.Reader, lat, lon, radiusKm float64) ([]float64, error) {
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		if err == io.EOF {
			return nil, nil // empty response is valid (no fires)
		}
		return nil, err
	}

	latIdx, lonIdx := -1, -1
	for i, col := range header {
		switch strings.ToLower(strings.TrimSpace(col)) {
		case "latitude":
			latIdx = i
		case "longitude":
			lonIdx = i
		}
	}
	if latIdx < 0 || lonIdx < 0 {
		return nil, fmt.Errorf("firms csv: missing latitude/longitude columns")
	}

	var dists []float64
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // skip malformed rows
		}
		if latIdx >= len(record) || lonIdx >= len(record) {
			continue
		}
		hlat, err := strconv.ParseFloat(strings.TrimSpace(record[latIdx]), 64)
		if err != nil {
			continue
		}
		hlon, err := strconv.ParseFloat(strings.TrimSpace(record[lonIdx]), 64)
		if err != nil {
			continue
		}
		dist := haversineKm(lat, lon, hlat, hlon)
		if dist <= radiusKm {
			dists = append(dists, dist)
		}
	}
	return dists, nil
}

// wildfireRisk returns a human-readable risk level based on nearest hotspot
// distance and count.
func wildfireRisk(nearestKm float64, count int) string {
	if count == 0 {
		return "none"
	}
	switch {
	case nearestKm < 10:
		return "extreme"
	case nearestKm < 25:
		return "high"
	case nearestKm < 50:
		return "moderate"
	default:
		return "low"
	}
}

// boundingBox returns a FIRMS-compatible bbox string (W,S,E,N) for a circle
// approximated by a square of radiusKm around a lat/lon.
func boundingBox(lat, lon, radiusKm float64) string {
	latDelta := radiusKm / 111.0
	lonDelta := radiusKm / (111.0 * math.Cos(lat*math.Pi/180))
	return fmt.Sprintf("%.4f,%.4f,%.4f,%.4f",
		lon-lonDelta, lat-latDelta,
		lon+lonDelta, lat+latDelta,
	)
}

// haversineKm returns the great-circle distance in km between two lat/lon pairs.
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthR = 6371.0
	phi1 := lat1 * math.Pi / 180
	phi2 := lat2 * math.Pi / 180
	dPhi := (lat2 - lat1) * math.Pi / 180
	dLambda := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dPhi/2)*math.Sin(dPhi/2) +
		math.Cos(phi1)*math.Cos(phi2)*math.Sin(dLambda/2)*math.Sin(dLambda/2)
	return earthR * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
