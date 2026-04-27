// netatmo.go polls Netatmo Weather Station modules via the Netatmo Connect API (C-5).
// Auth: OAuth 2.0 refresh-token flow — the service holds a long-lived refresh token
// and exchanges it for short-lived access tokens automatically.
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"polar/internal/config"
	"polar/pkg/contracts"
)

const (
	netatmoTokenURL    = "https://api.netatmo.com/oauth2/token"
	netatmoStationsURL = "https://api.netatmo.com/api/getstationsdata"

	// Module type constants from the Netatmo API.
	netatmoTypeBase        = "NAMain"    // indoor base station
	netatmoTypeOutdoor     = "NAModule1" // outdoor module
	netatmoTypeIndoorExtra = "NAModule4" // additional indoor module
	netatmoTypeWind        = "NAModule2" // wind gauge
	netatmoTypeRain        = "NAModule3" // rain gauge
)

// NetatmoService polls Netatmo Weather Station devices using the Connect API.
type NetatmoService struct {
	cfg       config.Config
	natCfg    config.NetatmoConfig
	http      *http.Client
	stationID string

	mu           sync.Mutex
	accessToken  string
	tokenExpires time.Time
}

func NewNetatmoService(cfg config.Config, httpClient *http.Client) *NetatmoService {
	return &NetatmoService{
		cfg:       cfg,
		natCfg:    cfg.Netatmo,
		http:      httpClient,
		stationID: cfg.Station.ID,
	}
}

func (s *NetatmoService) Collect() []contracts.Reading {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := s.ensureToken(ctx); err != nil {
		log.Printf("netatmo: token refresh failed: %v", err)
		return nil
	}

	devices, err := s.fetchStations(ctx)
	if err != nil {
		log.Printf("netatmo: fetch stations failed: %v", err)
		return nil
	}

	now := time.Now().UTC()
	var out []contracts.Reading

	for _, device := range devices {
		// Skip devices not in the allow-list (if configured).
		if len(s.natCfg.DeviceIDs) > 0 && !contains(s.natCfg.DeviceIDs, device.ID) {
			continue
		}

		// Base station readings.
		label := device.StationName
		if label == "" {
			label = device.ID
		}
		out = append(out, parseNetatmoData(s.stationID, label, "netatmo", netatmoTypeBase, device.DashboardData, now)...)

		// Module readings.
		for _, mod := range device.Modules {
			modLabel := mod.ModuleName
			if modLabel == "" {
				modLabel = mod.ID
			}
			out = append(out, parseNetatmoData(s.stationID, modLabel, "netatmo", mod.Type, mod.DashboardData, now)...)
		}
	}

	return out
}

func (s *NetatmoService) Metrics() []string {
	return []string{"temperature", "humidity", "co2", "noise", "pressure", "wind_speed", "rain"}
}

// ensureToken obtains or refreshes the OAuth2 access token when expired.
func (s *NetatmoService) ensureToken(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.accessToken != "" && time.Now().Before(s.tokenExpires.Add(-30*time.Second)) {
		return nil // still valid
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", s.natCfg.RefreshToken)
	form.Set("client_id", s.natCfg.ClientID)
	form.Set("client_secret", s.natCfg.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, netatmoTokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("netatmo token: status %d: %s", resp.StatusCode, string(body))
	}

	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return err
	}
	if tok.Error != "" {
		return fmt.Errorf("netatmo token error: %s — %s", tok.Error, tok.ErrorDesc)
	}

	s.accessToken = tok.AccessToken
	s.tokenExpires = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)

	// Netatmo rotates the refresh token on each exchange — persist the new one.
	if tok.RefreshToken != "" && tok.RefreshToken != s.natCfg.RefreshToken {
		log.Printf("netatmo: refresh token rotated (update POLAR_NETATMO_REFRESH_TOKEN)")
		s.natCfg.RefreshToken = tok.RefreshToken
	}

	log.Printf("netatmo: access token refreshed (expires in %ds)", tok.ExpiresIn)
	return nil
}

// netatmoDevice models a Netatmo station from the getstationsdata response.
type netatmoDevice struct {
	ID            string         `json:"_id"`
	StationName   string         `json:"station_name"`
	DashboardData netatmoData    `json:"dashboard_data"`
	Modules       []netatmoModule `json:"modules"`
}

type netatmoModule struct {
	ID            string      `json:"_id"`
	ModuleName    string      `json:"module_name"`
	Type          string      `json:"type"`
	DashboardData netatmoData `json:"dashboard_data"`
}

// netatmoData holds the sensor values returned in dashboard_data.
// All fields are optional — a nil pointer means the sensor is absent.
type netatmoData struct {
	Temperature *float64 `json:"Temperature"`
	Humidity    *float64 `json:"Humidity"`
	CO2         *float64 `json:"CO2"`
	Noise       *float64 `json:"Noise"`
	Pressure    *float64 `json:"Pressure"`
	WindStrength *float64 `json:"WindStrength"` // km/h
	WindAngle   *float64 `json:"WindAngle"`
	GustStrength *float64 `json:"GustStrength"`
	Rain        *float64 `json:"Rain"`       // mm/h
	SumRain1    *float64 `json:"sum_rain_1"` // mm in last hour
}

func (s *NetatmoService) fetchStations(ctx context.Context) ([]netatmoDevice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, netatmoStationsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.accessToken)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("netatmo getstationsdata: status %d", resp.StatusCode)
	}

	var payload struct {
		Body struct {
			Devices []netatmoDevice `json:"devices"`
		} `json:"body"`
		Status string `json:"status"`
		Error  struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Status != "ok" {
		return nil, fmt.Errorf("netatmo api error %d: %s", payload.Error.Code, payload.Error.Message)
	}
	return payload.Body.Devices, nil
}

// parseNetatmoData converts a netatmoData block into Polar readings.
func parseNetatmoData(stationID, sensorID, source, moduleType string, d netatmoData, now time.Time) []contracts.Reading {
	var out []contracts.Reading

	reading := func(metric string, value float64, unit string) contracts.Reading {
		return contracts.Reading{
			StationID:   stationID,
			SensorID:    sensorID,
			Metric:      metric,
			Value:       value,
			Unit:        unit,
			Source:      source,
			QualityFlag: contracts.QualityGood,
			RecordedAt:  now,
			ReceivedAt:  now,
		}
	}

	if d.Temperature != nil {
		out = append(out, reading("temperature", *d.Temperature, "C"))
	}
	if d.Humidity != nil {
		out = append(out, reading("humidity", *d.Humidity, "%RH"))
	}
	if d.CO2 != nil {
		out = append(out, reading("co2", *d.CO2, "ppm"))
	}
	if d.Noise != nil {
		out = append(out, reading("noise", *d.Noise, "dB"))
	}
	if d.Pressure != nil {
		out = append(out, reading("pressure", *d.Pressure, "hPa"))
	}

	// Wind module (NAModule2).
	if moduleType == netatmoTypeWind && d.WindStrength != nil {
		out = append(out, reading("wind_speed", *d.WindStrength, "km/h"))
	}
	if moduleType == netatmoTypeWind && d.GustStrength != nil {
		out = append(out, reading("wind_gust", *d.GustStrength, "km/h"))
	}

	// Rain module (NAModule3).
	if moduleType == netatmoTypeRain && d.Rain != nil {
		out = append(out, reading("rain", *d.Rain, "mm/h"))
	}
	if moduleType == netatmoTypeRain && d.SumRain1 != nil {
		out = append(out, reading("rain_1h", *d.SumRain1, "mm"))
	}

	return out
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
