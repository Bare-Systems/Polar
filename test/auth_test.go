// auth_test.go — X-2: Authorization negative test suite.
//
// Covers:
//   - No token → 401 on every protected REST route
//   - Wrong scope → 403 on every scope boundary
//   - Expired token → 401 on any protected route
//   - Cross-target isolation (X-5) — token with allowed_targets can't access
//     a forbidden target via REST or MCP
//   - WebSocket upgrade rejected without auth or with wrong scope
//   - MCP methods return -32003 when called with insufficient scope
package test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"polar/internal/auth"
	"polar/internal/config"
)

// --- helpers ---

// authCfg builds an AuthConfig from a slice of named (name, token, scopes…) tuples.
func makeTokens(tokens ...config.TokenConfig) config.AuthConfig {
	return config.AuthConfig{Tokens: tokens}
}

func tok(name, value string, scopes ...string) config.TokenConfig {
	return config.TokenConfig{Name: name, Value: value, Scopes: scopes}
}

func tokWithTargets(name, value string, targets []string, scopes ...string) config.TokenConfig {
	return config.TokenConfig{
		Name:           name,
		Value:          value,
		Scopes:         scopes,
		AllowedTargets: targets,
	}
}

func tokExpired(name, value string, scopes ...string) config.TokenConfig {
	past := time.Now().UTC().Add(-time.Hour)
	return config.TokenConfig{Name: name, Value: value, Scopes: scopes, ExpiresAt: &past}
}

func twoTargetCfg() config.Config {
	cfg := baseConfig()
	cfg.Monitoring = config.MonitoringConfig{
		DefaultTargetID: "home",
		Targets: []config.MonitorTargetConfig{
			{ID: "home", DisplayName: "Home", Latitude: 1, Longitude: 1, IncludeIndoor: true, EnableWeather: true, EnableAirQuality: true},
			{ID: "cabin", DisplayName: "Cabin", Latitude: 2, Longitude: 2, EnableWeather: true, EnableAirQuality: true},
		},
	}
	return cfg
}

// mcpCall issues a JSON-RPC request to the MCP handler and returns the recorder.
func mcpCall(t *testing.T, handler http.Handler, token, method string, params map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func mcpErrorCode(t *testing.T, rr *httptest.ResponseRecorder) int {
	t.Helper()
	var resp struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("mcpErrorCode: json decode failed: %v\nbody: %s", err, rr.Body.String())
	}
	return resp.Error.Code
}

// --- X-2.1: No token → 401 on every protected REST route ---

func TestRESTNoTokenReturns401(t *testing.T) {
	cfg := twoTargetCfg()
	cfg.Auth = makeTokens(tok("admin", "admin-token", auth.WildcardScope))
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	handler := server.Handler()

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/capabilities"},
		{http.MethodGet, "/v1/station/health"},
		{http.MethodGet, "/v1/readings/latest"},
		{http.MethodGet, "/v1/readings?metric=temperature"},
		{http.MethodGet, "/v1/targets"},
		{http.MethodGet, "/v1/diagnostics/data-gaps"},
		{http.MethodGet, "/v1/climate/snapshot"},
		{http.MethodGet, "/v1/weather/current"},
		{http.MethodGet, "/v1/weather/alerts"},
		{http.MethodGet, "/v1/air-quality/current"},
		{http.MethodGet, "/v1/astronomy/today"},
		{http.MethodGet, "/v1/wildfire/current"},
		{http.MethodGet, "/v1/pollen/current"},
		{http.MethodGet, "/v1/uv/current"},
		{http.MethodGet, "/v1/air-quality/neighborhood"},
		{http.MethodGet, "/v1/forecast/latest"},
		{http.MethodGet, "/v1/weather/forecast"},
		{http.MethodGet, "/v1/air-quality/forecast"},
		{http.MethodGet, "/v1/audit/events"},
		{http.MethodGet, "/v1/metrics"},
		{http.MethodGet, "/v1/providers/licenses"},
		{http.MethodPost, "/v1/auth/token"},
		{http.MethodGet, "/v1/consent/grants"},
		// D-1: command endpoints require write:commands.
		{http.MethodPost, "/v1/commands"},
		{http.MethodGet, "/v1/commands"},
		{http.MethodGet, "/v1/commands/cmd_fake"},
	}

	for _, tc := range routes {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", rr.Code)
			}
		})
	}
}

// --- X-2.2: Wrong scope → 403 ---

func TestRESTWrongScopeReturns403(t *testing.T) {
	cfg := twoTargetCfg()
	cfg.Auth = makeTokens(
		tok("telemetry", "tel-token", auth.ScopeReadTelemetry),
		tok("forecast", "fc-token", auth.ScopeReadForecast),
		tok("audit", "audit-token", auth.ScopeReadAudit),
	)
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	handler := server.Handler()

	cases := []struct {
		token string
		path  string
		desc  string
	}{
		// Telemetry token cannot access forecast endpoints.
		{"tel-token", "/v1/forecast/latest", "telemetry→forecast"},
		{"tel-token", "/v1/weather/forecast", "telemetry→weather/forecast"},
		{"tel-token", "/v1/air-quality/forecast", "telemetry→aq/forecast"},
		// Telemetry token cannot access audit endpoint.
		{"tel-token", "/v1/audit/events", "telemetry→audit"},
		// Telemetry token cannot access admin endpoints.
		{"tel-token", "/v1/metrics", "telemetry→metrics"},
		{"tel-token", "/v1/providers/licenses", "telemetry→licenses"},
		// Forecast token cannot access telemetry endpoints.
		{"fc-token", "/v1/readings/latest", "forecast→telemetry"},
		{"fc-token", "/v1/weather/current", "forecast→weather/current"},
		// Audit token cannot access telemetry endpoints.
		{"audit-token", "/v1/readings/latest", "audit→telemetry"},
		{"audit-token", "/v1/metrics", "audit→admin"},
		// Telemetry token cannot submit commands (write:commands required).
		{"tel-token", "/v1/commands", "telemetry→commands"},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("%s: expected 403, got %d", tc.desc, rr.Code)
			}
		})
	}
}

// --- X-2.3: Expired token → 401 ---

func TestExpiredTokenReturns401(t *testing.T) {
	cfg := twoTargetCfg()
	cfg.Auth = makeTokens(tokExpired("expired", "expired-token", auth.ScopeReadTelemetry))
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	handler := server.Handler()

	routes := []string{
		"/v1/readings/latest",
		"/v1/weather/current",
		"/v1/climate/snapshot",
	}
	for _, path := range routes {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer expired-token")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401 for expired token on %s, got %d", path, rr.Code)
			}
		})
	}
}

// --- X-2.4 / X-5: Target isolation — REST ---

func TestTargetIsolationREST(t *testing.T) {
	cfg := twoTargetCfg()
	// Token scoped only to "home"; "cabin" is forbidden.
	cfg.Auth = makeTokens(
		tokWithTargets("home-only", "home-token", []string{"home"}, auth.ScopeReadTelemetry, auth.ScopeReadForecast),
		tok("unrestricted", "all-token", auth.ScopeReadTelemetry, auth.ScopeReadForecast),
	)
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	handler := server.Handler()

	targetRoutes := []string{
		"/v1/weather/current",
		"/v1/weather/alerts",
		"/v1/climate/snapshot",
		"/v1/astronomy/today",
		"/v1/wildfire/current",
		"/v1/pollen/current",
		"/v1/uv/current",
		"/v1/air-quality/current",
		"/v1/air-quality/neighborhood",
		"/v1/forecast/latest",
		"/v1/weather/forecast",
		"/v1/air-quality/forecast",
	}

	for _, path := range targetRoutes {
		t.Run("forbidden_"+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path+"?target=cabin", nil)
			req.Header.Set("Authorization", "Bearer home-token")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("expected 403 for home-token on cabin target at %s, got %d", path, rr.Code)
			}
		})

		t.Run("allowed_"+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path+"?target=home", nil)
			req.Header.Set("Authorization", "Bearer home-token")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			// May return 200 (data present) or 500/404 (no data seeded); must NOT be 401/403.
			if rr.Code == http.StatusUnauthorized || rr.Code == http.StatusForbidden {
				t.Fatalf("expected access allowed for home-token on home target at %s, got %d", path, rr.Code)
			}
		})

		t.Run("default_resolves_to_home_"+path, func(t *testing.T) {
			// Omitting ?target= should resolve to "home" (the configured default) and be allowed.
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer home-token")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code == http.StatusUnauthorized || rr.Code == http.StatusForbidden {
				t.Fatalf("expected access for home-token on default target at %s, got %d", path, rr.Code)
			}
		})

		t.Run("unrestricted_allows_cabin_"+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path+"?target=cabin", nil)
			req.Header.Set("Authorization", "Bearer all-token")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code == http.StatusUnauthorized || rr.Code == http.StatusForbidden {
				t.Fatalf("expected unrestricted token to access cabin at %s, got %d", path, rr.Code)
			}
		})
	}
}

// --- X-2.5: WebSocket requires auth ---

func TestWebSocketRequiresAuth(t *testing.T) {
	cfg := twoTargetCfg()
	cfg.Auth = makeTokens(
		tok("telemetry", "tel-token", auth.ScopeReadTelemetry),
		tok("forecast-only", "fc-token", auth.ScopeReadForecast),
	)
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	wsBase := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/live?target=home"

	t.Run("no_token_rejected", func(t *testing.T) {
		_, resp, err := websocket.DefaultDialer.Dial(wsBase, nil)
		if err == nil {
			t.Fatal("expected dial to fail without token, but it succeeded")
		}
		if resp != nil && resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})

	t.Run("wrong_scope_rejected", func(t *testing.T) {
		_, resp, err := websocket.DefaultDialer.Dial(wsBase, http.Header{
			"Authorization": []string{"Bearer fc-token"},
		})
		if err == nil {
			t.Fatal("expected dial to fail with forecast-only token, but it succeeded")
		}
		if resp != nil && resp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", resp.StatusCode)
		}
	})

	t.Run("valid_token_accepted", func(t *testing.T) {
		conn, _, err := websocket.DefaultDialer.Dial(wsBase, http.Header{
			"Authorization": []string{"Bearer tel-token"},
		})
		if err != nil {
			t.Fatalf("expected successful dial with telemetry token, got: %v", err)
		}
		conn.Close()
	})
}

// --- X-2.6: WebSocket target isolation ---

func TestWebSocketTargetIsolation(t *testing.T) {
	cfg := twoTargetCfg()
	cfg.Auth = makeTokens(
		tokWithTargets("home-only", "home-ws-token", []string{"home"}, auth.ScopeReadTelemetry),
	)
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	wsBase := "ws" + strings.TrimPrefix(ts.URL, "http")

	t.Run("forbidden_target_rejected", func(t *testing.T) {
		_, resp, err := websocket.DefaultDialer.Dial(wsBase+"/v1/live?target=cabin", http.Header{
			"Authorization": []string{"Bearer home-ws-token"},
		})
		if err == nil {
			t.Fatal("expected dial to fail for forbidden target")
		}
		if resp != nil && resp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", resp.StatusCode)
		}
	})

	t.Run("allowed_target_accepted", func(t *testing.T) {
		conn, _, err := websocket.DefaultDialer.Dial(wsBase+"/v1/live?target=home", http.Header{
			"Authorization": []string{"Bearer home-ws-token"},
		})
		if err != nil {
			t.Fatalf("expected success for allowed target, got: %v", err)
		}
		conn.Close()
	})
}

// --- X-2.7: MCP no token → HTTP 401 ---

func TestMCPNoTokenReturnsHTTP401(t *testing.T) {
	cfg := twoTargetCfg()
	cfg.Auth = makeTokens(tok("admin", "admin-token", auth.WildcardScope))
	_, _, _, mcpServer := newTestStack(t, cfg, stubForecastClient{})
	handler := mcpServer.Handler()

	rr := mcpCall(t, handler, "", "list_capabilities", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// --- X-2.8: MCP wrong scope → JSON-RPC -32003 ---

func TestMCPWrongScopeReturnsRPCError(t *testing.T) {
	cfg := twoTargetCfg()
	cfg.Auth = makeTokens(
		tok("telemetry", "tel-token", auth.ScopeReadTelemetry),
		tok("forecast", "fc-token", auth.ScopeReadForecast),
		tok("audit", "audit-token", auth.ScopeReadAudit),
	)
	_, _, _, mcpServer := newTestStack(t, cfg, stubForecastClient{})
	handler := mcpServer.Handler()

	cases := []struct {
		token  string
		method string
		desc   string
	}{
		// Telemetry token cannot call forecast methods.
		{"tel-token", "get_forecast", "telemetry→get_forecast"},
		{"tel-token", "get_weather_forecast", "telemetry→get_weather_forecast"},
		{"tel-token", "get_air_quality_forecast", "telemetry→get_air_quality_forecast"},
		// Telemetry token cannot call audit methods.
		{"tel-token", "get_audit_events", "telemetry→get_audit_events"},
		// Telemetry token cannot call admin methods.
		{"tel-token", "get_metrics", "telemetry→get_metrics"},
		{"tel-token", "list_provider_licenses", "telemetry→list_provider_licenses"},
		// Forecast token cannot call telemetry methods.
		{"fc-token", "get_latest_readings", "forecast→get_latest_readings"},
		{"fc-token", "get_weather_current", "forecast→get_weather_current"},
		{"fc-token", "get_climate_snapshot", "forecast→get_climate_snapshot"},
		// Audit token cannot call telemetry methods.
		{"audit-token", "get_latest_readings", "audit→get_latest_readings"},
		{"audit-token", "get_metrics", "audit→get_metrics"},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			rr := mcpCall(t, handler, tc.token, tc.method, map[string]any{"target": "home"})
			if rr.Code != http.StatusOK {
				t.Fatalf("%s: expected HTTP 200 envelope, got %d", tc.desc, rr.Code)
			}
			code := mcpErrorCode(t, rr)
			if code != -32003 {
				t.Fatalf("%s: expected RPC error -32003, got %d (body: %s)", tc.desc, code, rr.Body.String())
			}
		})
	}
}

// --- X-2.9: MCP expired token → HTTP 401 ---

func TestMCPExpiredTokenReturnsHTTP401(t *testing.T) {
	cfg := twoTargetCfg()
	cfg.Auth = makeTokens(tokExpired("expired", "expired-mcp-token", auth.ScopeReadTelemetry))
	_, _, _, mcpServer := newTestStack(t, cfg, stubForecastClient{})

	rr := mcpCall(t, mcpServer.Handler(), "expired-mcp-token", "list_capabilities", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired MCP token, got %d", rr.Code)
	}
}

// --- X-2.10 / X-5: MCP target isolation ---

func TestMCPTargetIsolation(t *testing.T) {
	cfg := twoTargetCfg()
	cfg.Auth = makeTokens(
		tokWithTargets("home-only", "home-mcp-token", []string{"home"},
			auth.ScopeReadTelemetry, auth.ScopeReadForecast),
		tok("unrestricted", "all-mcp-token",
			auth.ScopeReadTelemetry, auth.ScopeReadForecast),
	)
	_, _, _, mcpServer := newTestStack(t, cfg, stubForecastClient{})
	handler := mcpServer.Handler()

	targetMethods := []string{
		"get_weather_current",
		"get_weather_alerts",
		"get_climate_snapshot",
		"get_forecast",
		"get_weather_forecast",
		"get_air_quality_current",
		"get_air_quality_forecast",
		"get_astronomy",
		"get_wildfire_context",
		"get_pollen",
		"get_uv",
		"get_neighborhood_aq",
	}

	for _, method := range targetMethods {
		t.Run("forbidden_"+method, func(t *testing.T) {
			rr := mcpCall(t, handler, "home-mcp-token", method, map[string]any{"target": "cabin"})
			if rr.Code != http.StatusOK {
				t.Fatalf("%s: expected HTTP 200 envelope, got %d", method, rr.Code)
			}
			code := mcpErrorCode(t, rr)
			if code != -32003 {
				t.Fatalf("%s: expected -32003 for forbidden target, got %d (body: %s)", method, code, rr.Body.String())
			}
		})

		t.Run("allowed_"+method, func(t *testing.T) {
			rr := mcpCall(t, handler, "home-mcp-token", method, map[string]any{"target": "home"})
			if rr.Code != http.StatusOK {
				t.Fatalf("%s: expected HTTP 200, got %d", method, rr.Code)
			}
			// May have a result error (no data) but must NOT be an auth/scope error.
			code := mcpErrorCode(t, rr)
			if code == -32003 {
				t.Fatalf("%s: got unexpected auth error -32003 for allowed target (body: %s)", method, rr.Body.String())
			}
		})

		t.Run("unrestricted_allows_cabin_"+method, func(t *testing.T) {
			rr := mcpCall(t, handler, "all-mcp-token", method, map[string]any{"target": "cabin"})
			if rr.Code != http.StatusOK {
				t.Fatalf("%s: expected HTTP 200, got %d", method, rr.Code)
			}
			code := mcpErrorCode(t, rr)
			if code == -32003 {
				t.Fatalf("%s: unrestricted token should not get auth error for cabin (body: %s)", method, rr.Body.String())
			}
		})
	}
}

// --- X-2.11: Unknown token → 401 ---

func TestUnknownTokenReturns401(t *testing.T) {
	cfg := twoTargetCfg()
	cfg.Auth = makeTokens(tok("real", "real-token", auth.ScopeReadTelemetry))
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})

	req := httptest.NewRequest(http.MethodGet, "/v1/readings/latest", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unknown token, got %d", rr.Code)
	}
}

// --- X-2.12: Wildcard scope passes all scope checks ---

func TestWildcardScopePassesAll(t *testing.T) {
	cfg := twoTargetCfg()
	cfg.Auth = makeTokens(tok("super", "super-token", auth.WildcardScope))
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	handler := server.Handler()

	scopedRoutes := []string{
		"/v1/readings/latest",
		"/v1/forecast/latest",
		"/v1/audit/events",
		"/v1/metrics",
		"/v1/providers/licenses",
	}
	for _, path := range scopedRoutes {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer super-token")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code == http.StatusUnauthorized || rr.Code == http.StatusForbidden {
				t.Fatalf("wildcard token should pass all scope checks on %s, got %d", path, rr.Code)
			}
		})
	}
}
