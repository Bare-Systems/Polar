package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"polar/pkg/contracts"
)

type AirQualityClient interface {
	FetchCurrent(ctx context.Context, target contracts.MonitorTarget) (contracts.AirQualityCurrent, error)
	FetchForecast(ctx context.Context, target contracts.MonitorTarget) (contracts.AirQualityForecast, error)
}

type AirNowClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewAirNowClient(baseURL, token string, httpClient *http.Client) *AirNowClient {
	return &AirNowClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    httpClient,
	}
}

func (c *AirNowClient) FetchCurrent(ctx context.Context, target contracts.MonitorTarget) (contracts.AirQualityCurrent, error) {
	if strings.TrimSpace(c.token) == "" {
		return contracts.AirQualityCurrent{}, fmt.Errorf("airnow token not configured")
	}

	reqURL, err := c.buildURL("/observation/latLong/current/", target)
	if err != nil {
		return contracts.AirQualityCurrent{}, err
	}
	var payload []struct {
		DateObserved  string `json:"DateObserved"`
		HourObserved  int    `json:"HourObserved"`
		LocalTimeZone string `json:"LocalTimeZone"`
		ReportingArea string `json:"ReportingArea"`
		StateCode     string `json:"StateCode"`
		ParameterName string `json:"ParameterName"`
		AQI           int    `json:"AQI"`
		Category      struct {
			Name string `json:"Name"`
		} `json:"Category"`
	}
	if err := c.getJSON(ctx, reqURL, &payload); err != nil {
		return contracts.AirQualityCurrent{}, err
	}
	if len(payload) == 0 {
		return contracts.AirQualityCurrent{}, fmt.Errorf("airnow current observation unavailable")
	}

	pollutants := make([]contracts.AirQualityPollutant, 0, len(payload))
	bestAQI := -1
	bestCategory := ""
	recordedAt := time.Now().UTC()
	reportingArea := payload[0].ReportingArea
	stateCode := payload[0].StateCode
	for _, item := range payload {
		if item.AQI > bestAQI {
			bestAQI = item.AQI
			bestCategory = item.Category.Name
		}
		if t, err := airNowObservedAt(item.DateObserved, item.HourObserved); err == nil {
			recordedAt = t
		}
		pollutants = append(pollutants, contracts.AirQualityPollutant{
			Code:     normalizePollutantCode(item.ParameterName),
			Name:     item.ParameterName,
			AQI:      item.AQI,
			Category: item.Category.Name,
		})
	}
	sort.Slice(pollutants, func(i, j int) bool {
		return pollutants[i].AQI > pollutants[j].AQI
	})
	if len(pollutants) > 0 {
		pollutants[0].Primary = true
	}

	return contracts.AirQualityCurrent{
		TargetID:      target.ID,
		Source:        "airnow",
		RecordedAt:    recordedAt,
		FetchedAt:     time.Now().UTC(),
		ReportingArea: reportingArea,
		StateCode:     stateCode,
		OverallAQI:    maxInt(bestAQI, 0),
		Category:      bestCategory,
		Pollutants:    pollutants,
	}, nil
}

func (c *AirNowClient) FetchForecast(ctx context.Context, target contracts.MonitorTarget) (contracts.AirQualityForecast, error) {
	if strings.TrimSpace(c.token) == "" {
		return contracts.AirQualityForecast{}, fmt.Errorf("airnow token not configured")
	}

	reqURL, err := c.buildURL("/forecast/latLong/", target)
	if err != nil {
		return contracts.AirQualityForecast{}, err
	}
	var payload []struct {
		DateForecast  string `json:"DateForecast"`
		ParameterName string `json:"ParameterName"`
		AQI           int    `json:"AQI"`
		ActionDay     bool   `json:"ActionDay"`
		Discussion    string `json:"Discussion"`
		Category      struct {
			Name string `json:"Name"`
		} `json:"Category"`
	}
	if err := c.getJSON(ctx, reqURL, &payload); err != nil {
		return contracts.AirQualityForecast{}, err
	}

	periods := make([]contracts.AirQualityForecastPeriod, 0, len(payload))
	for _, item := range payload {
		periods = append(periods, contracts.AirQualityForecastPeriod{
			Date:       item.DateForecast,
			Parameter:  item.ParameterName,
			AQI:        item.AQI,
			Category:   item.Category.Name,
			ActionDay:  item.ActionDay,
			Discussion: item.Discussion,
		})
	}
	sort.Slice(periods, func(i, j int) bool {
		if periods[i].Date == periods[j].Date {
			return periods[i].AQI > periods[j].AQI
		}
		return periods[i].Date < periods[j].Date
	})

	return contracts.AirQualityForecast{
		TargetID:  target.ID,
		Source:    "airnow",
		FetchedAt: time.Now().UTC(),
		Periods:   periods,
	}, nil
}

func (c *AirNowClient) buildURL(path string, target contracts.MonitorTarget) (string, error) {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("format", "application/json")
	q.Set("latitude", strconv.FormatFloat(target.Latitude, 'f', 4, 64))
	q.Set("longitude", strconv.FormatFloat(target.Longitude, 'f', 4, 64))
	q.Set("distance", "25")
	q.Set("API_KEY", c.token)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *AirNowClient) getJSON(ctx context.Context, reqURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("airnow status: %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func airNowObservedAt(dateObserved string, hourObserved int) (time.Time, error) {
	return time.Parse(time.RFC3339, fmt.Sprintf("%sT%02d:00:00Z", dateObserved, hourObserved))
}

func normalizePollutantCode(name string) string {
	code := strings.ToLower(strings.ReplaceAll(name, ".", ""))
	code = strings.ReplaceAll(code, " ", "_")
	return code
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
