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

type ForecastClient interface {
	Fetch(ctx context.Context, target contracts.MonitorTarget, endpoint string) (contracts.ForecastSnapshot, error)
}

type OpenMeteoClient struct {
	http *http.Client
}

func NewOpenMeteoClient(httpClient *http.Client) *OpenMeteoClient {
	return &OpenMeteoClient{http: httpClient}
}

type openMeteoResp struct {
	Hourly struct {
		Time               []string  `json:"time"`
		Temperature2M      []float64 `json:"temperature_2m"`
		RelativeHumidity2M []float64 `json:"relative_humidity_2m"`
		WindSpeed10M       []float64 `json:"wind_speed_10m"`
		Precipitation      []float64 `json:"precipitation"`
	} `json:"hourly"`
}

func (c *OpenMeteoClient) Fetch(ctx context.Context, target contracts.MonitorTarget, endpoint string) (contracts.ForecastSnapshot, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return contracts.ForecastSnapshot{}, err
	}
	q := u.Query()
	q.Set("latitude", strconv.FormatFloat(target.Latitude, 'f', 4, 64))
	q.Set("longitude", strconv.FormatFloat(target.Longitude, 'f', 4, 64))
	q.Set("hourly", "temperature_2m,relative_humidity_2m,wind_speed_10m,precipitation")
	q.Set("forecast_days", "2")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return contracts.ForecastSnapshot{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return contracts.ForecastSnapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return contracts.ForecastSnapshot{}, fmt.Errorf("open-meteo status: %d", resp.StatusCode)
	}

	var payload openMeteoResp
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return contracts.ForecastSnapshot{}, err
	}

	points := make([]contracts.ForecastPoint, 0, len(payload.Hourly.Time))
	for i := range payload.Hourly.Time {
		tm, err := time.Parse(time.RFC3339, payload.Hourly.Time[i]+":00Z")
		if err != nil {
			continue
		}
		p := contracts.ForecastPoint{Time: tm}
		if i < len(payload.Hourly.Temperature2M) {
			p.TemperatureC = payload.Hourly.Temperature2M[i]
		}
		if i < len(payload.Hourly.RelativeHumidity2M) {
			p.HumidityPct = payload.Hourly.RelativeHumidity2M[i]
		}
		if i < len(payload.Hourly.WindSpeed10M) {
			p.WindSpeedMS = payload.Hourly.WindSpeed10M[i]
		}
		if i < len(payload.Hourly.Precipitation) {
			p.PrecipMM = payload.Hourly.Precipitation[i]
		}
		points = append(points, p)
	}

	return contracts.ForecastSnapshot{
		TargetID:   target.ID,
		StationID:  target.ID,
		Provider:   "open-meteo",
		Latitude:   target.Latitude,
		Longitude:  target.Longitude,
		FetchedAt:  time.Now().UTC(),
		Points:     points,
		FreshUntil: time.Now().UTC().Add(2 * time.Hour),
	}, nil
}
