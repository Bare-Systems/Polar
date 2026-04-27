// switchbot.go polls SwitchBot devices via the SwitchBot OpenAPI v1.1 (B-2).
// Auth: token + HMAC-SHA256(secret, token + nonce + timestamp) as per the spec.
package collector

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"polar/internal/config"
	"polar/pkg/contracts"
)

const switchBotAPIBase = "https://api.switch-bot.com/v1.1"

// SwitchBotService polls SwitchBot devices using the OpenAPI.
type SwitchBotService struct {
	cfg       config.Config
	sbCfg     config.SwitchBotConfig
	http      *http.Client
	stationID string
	baseURL   string // overridable for testing; defaults to switchBotAPIBase
}

func NewSwitchBotService(cfg config.Config, httpClient *http.Client) *SwitchBotService {
	return &SwitchBotService{
		cfg:       cfg,
		sbCfg:     cfg.SwitchBot,
		http:      httpClient,
		stationID: cfg.Station.ID,
		baseURL:   switchBotAPIBase,
	}
}

// SetBaseURL overrides the SwitchBot API base URL. Used in tests only.
func (s *SwitchBotService) SetBaseURL(u string) { s.baseURL = u }

func (s *SwitchBotService) Collect() []contracts.Reading {
	var out []contracts.Reading
	for _, dev := range s.sbCfg.Devices {
		if !dev.Enabled {
			continue
		}
		readings, err := s.pollDevice(dev)
		if err != nil {
			log.Printf("switchbot: device %s (%s) error: %v", dev.Label, dev.DeviceID, err)
			continue
		}
		out = append(out, readings...)
	}
	return out
}

func (s *SwitchBotService) Metrics() []string {
	return []string{"temperature", "humidity", "co2"}
}

func (s *SwitchBotService) pollDevice(dev config.SwitchBotDevice) ([]contracts.Reading, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reqURL := fmt.Sprintf("%s/devices/%s/status", s.baseURL, dev.DeviceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	s.signRequest(req)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("switchbot device %s: status %d", dev.DeviceID, resp.StatusCode)
	}

	var payload struct {
		StatusCode int    `json:"statusCode"`
		Message    string `json:"message"`
		Body       struct {
			DeviceID    string  `json:"deviceId"`
			DeviceType  string  `json:"deviceType"`
			Temperature float64 `json:"temperature"`
			Humidity    float64 `json:"humidity"`
			CO2         float64 `json:"CO2"`
		} `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.StatusCode != 100 {
		return nil, fmt.Errorf("switchbot api error: %s", payload.Message)
	}

	now := time.Now().UTC()
	sensorID := dev.DeviceID
	deviceType := strings.ToLower(payload.Body.DeviceType)
	if dev.Label != "" {
		sensorID = dev.Label
	}

	var out []contracts.Reading
	if payload.Body.Temperature != 0 {
		out = append(out, contracts.Reading{
			StationID:   s.stationID,
			SensorID:    sensorID,
			Metric:      "temperature",
			Value:       payload.Body.Temperature,
			Unit:        "C",
			Source:      "switchbot",
			QualityFlag: contracts.QualityGood,
			RecordedAt:  now,
			ReceivedAt:  now,
		})
	}
	if payload.Body.Humidity != 0 {
		out = append(out, contracts.Reading{
			StationID:   s.stationID,
			SensorID:    sensorID,
			Metric:      "humidity",
			Value:       payload.Body.Humidity,
			Unit:        "%RH",
			Source:      "switchbot",
			QualityFlag: contracts.QualityGood,
			RecordedAt:  now,
			ReceivedAt:  now,
		})
	}
	// CO2 is available on CO2 Sensor and Hub 2 devices.
	if payload.Body.CO2 != 0 && strings.Contains(deviceType, "co2") {
		out = append(out, contracts.Reading{
			StationID:   s.stationID,
			SensorID:    sensorID,
			Metric:      "co2",
			Value:       payload.Body.CO2,
			Unit:        "ppm",
			Source:      "switchbot",
			QualityFlag: contracts.QualityGood,
			RecordedAt:  now,
			ReceivedAt:  now,
		})
	}
	return out, nil
}

// signRequest adds the SwitchBot OpenAPI v1.1 authentication headers to req.
// Signature: Base64(HMAC-SHA256(secret, token + nonce + timestamp))
func (s *SwitchBotService) signRequest(req *http.Request) {
	t := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := t // reuse timestamp as nonce for simplicity
	data := s.sbCfg.Token + t + nonce

	mac := hmac.New(sha256.New, []byte(s.sbCfg.Secret))
	mac.Write([]byte(data))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	req.Header.Set("Authorization", s.sbCfg.Token)
	req.Header.Set("t", t)
	req.Header.Set("nonce", nonce)
	req.Header.Set("sign", sign)
	req.Header.Set("Content-Type", "application/json")
}
