// pollen.go fetches pollen index and UV data from WeatherAPI.com (C-2, C-3).
// A single WeatherAPI call returns both pollen indices and UV index, so both
// providers are implemented in this file and share an HTTP client.
package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"polar/pkg/contracts"
)

// WeatherAPIClient fetches pollen and UV data from WeatherAPI.com.
type WeatherAPIClient struct {
	apiKey string
	http   *http.Client
}

const weatherAPIBase = "https://api.weatherapi.com/v1/forecast.json"

func NewWeatherAPIClient(apiKey string, httpClient *http.Client) *WeatherAPIClient {
	return &WeatherAPIClient{apiKey: apiKey, http: httpClient}
}

type weatherAPIResp struct {
	Forecast struct {
		Forecastday []struct {
			Day struct {
				UV        float64 `json:"uv"`
				AirQuality struct{} `json:"air_quality"`
				// Pollen indices are in the top-level day object.
				PollenTree  int `json:"daily_chance_of_rain"` // placeholder; real field below
			} `json:"day"`
		} `json:"forecastday"`
	} `json:"forecast"`
	// WeatherAPI returns pollen at the top-level current block for some plans;
	// for simplicity we parse from the forecast day.
	Current struct {
		UV      float64 `json:"uv"`
		Pollen struct {
			TreePollen  int `json:"tree_pollen"`
			GrassPollen int `json:"grass_pollen"`
			WeedPollen  int `json:"weed_pollen"`
		} `json:"pollen"`
	} `json:"current"`
}

// weatherAPIForecastDay is a more complete struct for parsing the API response.
type weatherAPIForecastDay struct {
	Date string `json:"date"`
	Day  struct {
		UV          float64 `json:"uv"`
		TreePollen  int     `json:"tree_pollen_index"`
		GrassPollen int     `json:"grass_pollen_index"`
		WeedPollen  int     `json:"weed_pollen_index"`
	} `json:"day"`
}

type weatherAPIFullResp struct {
	Current struct {
		UV float64 `json:"uv"`
	} `json:"current"`
	Forecast struct {
		Forecastday []weatherAPIForecastDay `json:"forecastday"`
	} `json:"forecast"`
}

// FetchPollen returns today's pollen context for the target.
func (c *WeatherAPIClient) FetchPollen(ctx context.Context, target contracts.MonitorTarget) (contracts.PollenContext, error) {
	data, err := c.fetch(ctx, target)
	if err != nil {
		return contracts.PollenContext{}, err
	}

	tree, grass, weed := 0, 0, 0
	if len(data.Forecast.Forecastday) > 0 {
		d := data.Forecast.Forecastday[0].Day
		tree = d.TreePollen
		grass = d.GrassPollen
		weed = d.WeedPollen
	}

	category := pollenCategory(max3(tree, grass, weed))
	return contracts.PollenContext{
		TreeIndex:  tree,
		GrassIndex: grass,
		WeedIndex:  weed,
		Category:   category,
		Source:     "weatherapi",
		UpdatedAt:  time.Now().UTC(),
	}, nil
}

// FetchUV returns today's UV index for the target.
func (c *WeatherAPIClient) FetchUV(ctx context.Context, target contracts.MonitorTarget) (contracts.UVContext, error) {
	data, err := c.fetch(ctx, target)
	if err != nil {
		return contracts.UVContext{}, err
	}
	uv := data.Current.UV
	return contracts.UVContext{
		UVIndex:   uv,
		Category:  uvCategory(uv),
		Source:    "weatherapi",
		UpdatedAt: time.Now().UTC(),
	}, nil
}

func (c *WeatherAPIClient) fetch(ctx context.Context, target contracts.MonitorTarget) (weatherAPIFullResp, error) {
	u, err := url.Parse(weatherAPIBase)
	if err != nil {
		return weatherAPIFullResp{}, err
	}
	q := u.Query()
	q.Set("key", c.apiKey)
	q.Set("q", fmt.Sprintf("%.4f,%.4f", target.Latitude, target.Longitude))
	q.Set("days", "1")
	q.Set("aqi", "no")
	q.Set("alerts", "no")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return weatherAPIFullResp{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return weatherAPIFullResp{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return weatherAPIFullResp{}, fmt.Errorf("weatherapi status: %d", resp.StatusCode)
	}
	var out weatherAPIFullResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return weatherAPIFullResp{}, err
	}
	return out, nil
}

func pollenCategory(index int) string {
	switch {
	case index == 0:
		return "None"
	case index <= 2:
		return "Low"
	case index <= 4:
		return "Moderate"
	case index <= 6:
		return "High"
	default:
		return "Very High"
	}
}

func uvCategory(uv float64) string {
	switch {
	case uv < 3:
		return "Low"
	case uv < 6:
		return "Moderate"
	case uv < 8:
		return "High"
	case uv < 11:
		return "Very High"
	default:
		return "Extreme"
	}
}

func max3(a, b, c int) int {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	return m
}
