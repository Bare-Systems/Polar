package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"polar/internal/config"
	"polar/pkg/contracts"
)

const (
	airthingsTokenURL    = "https://accounts-api.airthings.com/v1/token"
	airthingsAccountsURL = "https://consumer-api.airthings.com/v1/accounts"
	airthingsSamplesBase = "https://ext-api.airthings.com/v1/devices"
)

// airthingsMetricMap maps Airthings API field names to Polar metric names and units.
var airthingsMetricMap = map[string]struct {
	metric string
	unit   string
}{
	"temp":              {metric: "temperature", unit: "C"},
	"humidity":          {metric: "humidity", unit: "%RH"},
	"pressure":          {metric: "pressure", unit: "hPa"},
	"co2":               {metric: "co2", unit: "ppm"},
	"voc":               {metric: "voc", unit: "ppb"},
	"radonShortTermAvg": {metric: "radon", unit: "Bq/m3"},
	"pm25":              {metric: "pm25", unit: "ug/m3"},
	"pm1":               {metric: "pm1", unit: "ug/m3"},
	"light":             {metric: "light", unit: "lux"},
	"sla":               {metric: "sound_level", unit: "dB(A)"},
	"co":                {metric: "co", unit: "ppm"},
}

// AirthingsService implements collector.Service by polling the Airthings Consumer API.
type AirthingsService struct {
	cfg   config.Config
	atCfg config.AirthingsConfig
	http  *http.Client

	tokenMu     sync.Mutex
	token       string
	tokenExpiry time.Time

	// accountID is fetched once from /v1/accounts and cached for the lifetime
	// of the service. Used to build the account-scoped devices URL.
	accountMu sync.Mutex
	accountID string

	seenMu   sync.Mutex
	lastSeen map[string]int64 // device serial → last data unix timestamp
}

func NewAirthingsService(cfg config.Config, httpClient *http.Client) *AirthingsService {
	return &AirthingsService{
		cfg:      cfg,
		atCfg:    cfg.Airthings,
		http:     httpClient,
		lastSeen: make(map[string]int64),
	}
}

// Metrics returns the set of metric names this collector can produce.
// Used by core.Service.Capabilities() via duck typing.
func (s *AirthingsService) Metrics() []string {
	return []string{
		"temperature", "humidity", "pressure",
		"co2", "voc", "radon",
		"pm25", "pm1", "light", "sound_level", "co",
	}
}

// Collect fetches the latest readings from all configured (or discovered) Airthings
// devices and returns them as Polar readings. Returns nil on any unrecoverable error
// so the scheduler continues operating.
func (s *AirthingsService) Collect() []contracts.Reading {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	token, err := s.getToken(ctx)
	if err != nil {
		log.Printf("[airthings] token error: %v", err)
		return nil
	}

	deviceIDs, err := s.resolveDevices(ctx, token)
	if err != nil {
		log.Printf("[airthings] device discovery error: %v", err)
		return nil
	}

	now := time.Now().UTC()
	var readings []contracts.Reading

	for _, serial := range deviceIDs {
		partials, dataTS, err := s.fetchLatestSamples(ctx, token, serial)
		if err != nil {
			log.Printf("[airthings] samples error for %s: %v", serial, err)
			continue
		}

		s.seenMu.Lock()
		lastTS := s.lastSeen[serial]
		if dataTS > 0 && dataTS == lastTS {
			s.seenMu.Unlock()
			continue // device hasn't published new data since last poll
		}
		s.lastSeen[serial] = dataTS
		s.seenMu.Unlock()

		recordedAt := now
		if dataTS > 0 {
			recordedAt = time.Unix(dataTS, 0).UTC()
		}

		for _, p := range partials {
			readings = append(readings, contracts.Reading{
				StationID:   s.cfg.Station.ID,
				SensorID:    fmt.Sprintf("at-%s-%s", serial, p.metric),
				Metric:      p.metric,
				Value:       p.value,
				Unit:        p.unit,
				Source:      "airthings",
				QualityFlag: contracts.QualityGood,
				RecordedAt:  recordedAt,
				ReceivedAt:  now,
			})
		}
	}

	return readings
}

// ── OAuth2 token management ───────────────────────────────────────────────────

func (s *AirthingsService) getToken(ctx context.Context) (string, error) {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()

	if s.token != "" && time.Now().Before(s.tokenExpiry) {
		return s.token, nil
	}

	// Airthings token endpoint expects a JSON body with scope as an array.
	body, err := json.Marshal(map[string]any{
		"grant_type":    "client_credentials",
		"client_id":     s.atCfg.ClientID,
		"client_secret": s.atCfg.ClientSecret,
		"scope":         []string{"read:device:current_values"},
	})
	if err != nil {
		return "", fmt.Errorf("marshal token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, airthingsTokenURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("token request status %d", resp.StatusCode)
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	s.token = tok.AccessToken
	// Subtract 60s from expiry as a safety margin.
	s.tokenExpiry = time.Now().Add(time.Duration(tok.ExpiresIn-60) * time.Second)
	return s.token, nil
}

// ── Device discovery ──────────────────────────────────────────────────────────

// getAccountID fetches and caches the first account ID from /v1/accounts.
// The Airthings device list is scoped per account.
func (s *AirthingsService) getAccountID(ctx context.Context, token string) (string, error) {
	s.accountMu.Lock()
	defer s.accountMu.Unlock()

	if s.accountID != "" {
		return s.accountID, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, airthingsAccountsURL, nil)
	if err != nil {
		return "", fmt.Errorf("build accounts request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("accounts request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("accounts request status %d", resp.StatusCode)
	}

	var acctResp struct {
		Accounts []struct {
			ID string `json:"id"`
		} `json:"accounts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&acctResp); err != nil {
		return "", fmt.Errorf("decode accounts response: %w", err)
	}
	if len(acctResp.Accounts) == 0 {
		return "", fmt.Errorf("no accounts found for these credentials")
	}

	s.accountID = acctResp.Accounts[0].ID
	log.Printf("[airthings] using account %s", s.accountID)
	return s.accountID, nil
}

func (s *AirthingsService) resolveDevices(ctx context.Context, token string) ([]string, error) {
	if len(s.atCfg.DeviceIDs) > 0 {
		return s.atCfg.DeviceIDs, nil
	}

	accountID, err := s.getAccountID(ctx, token)
	if err != nil {
		return nil, err
	}

	u := fmt.Sprintf("%s/%s/devices", airthingsAccountsURL, accountID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build devices request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("devices request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("devices request status %d", resp.StatusCode)
	}

	var devResp struct {
		Devices []struct {
			SerialNumber string `json:"serialNumber"`
		} `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&devResp); err != nil {
		return nil, fmt.Errorf("decode devices response: %w", err)
	}

	serials := make([]string, 0, len(devResp.Devices))
	for _, d := range devResp.Devices {
		serials = append(serials, d.SerialNumber)
	}
	log.Printf("[airthings] discovered %d device(s): %v", len(serials), serials)
	return serials, nil
}

// ── Latest samples ────────────────────────────────────────────────────────────

type partialReading struct {
	metric string
	value  float64
	unit   string
}

func (s *AirthingsService) fetchLatestSamples(ctx context.Context, token, serial string) ([]partialReading, int64, error) {
	u := fmt.Sprintf("%s/%s/latest-samples", airthingsSamplesBase, serial)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build samples request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("samples request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("samples request status %d for %s", resp.StatusCode, serial)
	}

	var raw struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, 0, fmt.Errorf("decode samples response: %w", err)
	}

	var dataTS int64
	if t, ok := raw.Data["time"]; ok {
		if f, ok := t.(float64); ok {
			dataTS = int64(f)
		}
	}

	readings := make([]partialReading, 0, len(raw.Data))
	for field, mapping := range airthingsMetricMap {
		v, ok := raw.Data[field]
		if !ok {
			continue
		}
		f, ok := v.(float64)
		if !ok {
			continue
		}
		readings = append(readings, partialReading{
			metric: mapping.metric,
			value:  f,
			unit:   mapping.unit,
		})
	}

	return readings, dataTS, nil
}
