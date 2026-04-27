// shelly.go polls local Shelly Gen2+ devices via their HTTP RPC API (B-1).
// No Shelly Cloud dependency — all traffic is LAN-only.
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"polar/internal/config"
	"polar/pkg/contracts"
)

// ShellyService polls one or more Shelly devices over LAN HTTP.
type ShellyService struct {
	cfg        config.Config
	shelleyCfg config.ShellyConfig
	http       *http.Client
	stationID  string
}

func NewShellyService(cfg config.Config, httpClient *http.Client) *ShellyService {
	return &ShellyService{
		cfg:        cfg,
		shelleyCfg: cfg.Shelly,
		http:       httpClient,
		stationID:  cfg.Station.ID,
	}
}

func (s *ShellyService) Collect() []contracts.Reading {
	var out []contracts.Reading
	for _, dev := range s.shelleyCfg.Devices {
		if !dev.Enabled {
			continue
		}
		readings, err := s.pollDevice(dev)
		if err != nil {
			log.Printf("shelly: device %s (%s) error: %v", dev.Label, dev.IP, err)
			continue
		}
		out = append(out, readings...)
	}
	return out
}

func (s *ShellyService) Metrics() []string {
	return []string{"temperature", "humidity"}
}

func (s *ShellyService) pollDevice(dev config.ShellyDevice) ([]contracts.Reading, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now().UTC()
	sensorID := dev.ID
	if sensorID == "" {
		sensorID = dev.IP
	}

	var out []contracts.Reading

	// Temperature
	if temp, err := s.fetchRPC(ctx, dev.IP, "Temperature.GetStatus", 0); err == nil {
		if v, ok := temp["tC"].(float64); ok {
			out = append(out, contracts.Reading{
				StationID:   s.stationID,
				SensorID:    sensorID,
				Metric:      "temperature",
				Value:       v,
				Unit:        "C",
				Source:      "shelly",
				QualityFlag: contracts.QualityGood,
				RecordedAt:  now,
				ReceivedAt:  now,
			})
		}
	}

	// Humidity (not all Shelly devices have this sensor)
	if humid, err := s.fetchRPC(ctx, dev.IP, "Humidity.GetStatus", 0); err == nil {
		if v, ok := humid["rh"].(float64); ok {
			out = append(out, contracts.Reading{
				StationID:   s.stationID,
				SensorID:    sensorID,
				Metric:      "humidity",
				Value:       v,
				Unit:        "%RH",
				Source:      "shelly",
				QualityFlag: contracts.QualityGood,
				RecordedAt:  now,
				ReceivedAt:  now,
			})
		}
	}

	return out, nil
}

// fetchRPC calls a Shelly Gen2 RPC method over HTTP and returns the result map.
func (s *ShellyService) fetchRPC(ctx context.Context, ip, method string, id int) (map[string]any, error) {
	reqURL := fmt.Sprintf("http://%s/rpc/%s?id=%d", ip, method, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("shelly rpc %s: status %d", method, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
