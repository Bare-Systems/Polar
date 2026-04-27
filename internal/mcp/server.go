package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"polar/internal/auth"
	"polar/internal/config"
	"polar/internal/core"
	"polar/pkg/contracts"
)

type Server struct {
	cfg   config.Config
	svc   *core.Service
	authz *auth.Auth
}

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func NewServer(cfg config.Config, svc *core.Service, authz *auth.Auth) *Server {
	return &Server{cfg: cfg, svc: svc, authz: authz}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/healthz", s.instrument("mcp", "/healthz", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})))
	mux.Handle("/mcp", s.instrument("mcp", "/mcp", s.authz.Authenticate(http.HandlerFunc(s.rpc))))
	return mux
}

func (s *Server) rpc(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()

	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.svc.RecordRequestMetric("mcp", "invalid_json", http.StatusBadRequest, time.Since(startedAt))
		writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: nil, Error: map[string]any{"code": -32700, "message": "invalid json"}})
		return
	}

	result, errObj, status := s.dispatch(r, req.Method, req.Params)
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if errObj != nil {
		resp.Error = errObj
	} else {
		resp.Result = result
		status = http.StatusOK
	}
	s.svc.RecordRequestMetric("mcp", req.Method, status, time.Since(startedAt))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) dispatch(r *http.Request, method string, params map[string]any) (any, any, int) {
	if err := s.authz.AuthorizeRequest(r, methodScopes(method)...); err != nil {
		return nil, rpcErr(-32003, auth.Message(err)), auth.StatusCode(err)
	}

	// Resolve target: explicit param or fall back to configured default.
	target := paramString(params, "target")
	if target == "" && methodUsesTarget(method) {
		target = s.cfg.DefaultTargetID()
	}

	// Enforce per-target access control for all target-parameterized methods (X-5).
	if methodUsesTarget(method) {
		if err := s.authz.AuthorizeTarget(r, target); err != nil {
			return nil, rpcErr(-32003, auth.Message(err)), auth.StatusCode(err)
		}
	}

	switch method {
	case "list_capabilities":
		return s.svc.Capabilities(), nil, http.StatusOK
	case "list_targets":
		return s.svc.Targets(), nil, http.StatusOK
	case "get_station_health":
		return s.svc.StationHealth(), nil, http.StatusOK
	case "get_latest_readings":
		v, err := s.svc.LatestReadings(r.Context())
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return v, nil, http.StatusOK
	case "query_readings":
		metric := paramString(params, "metric")
		if metric == "" {
			return nil, rpcErr(-32602, "metric required"), http.StatusBadRequest
		}
		from := time.Now().UTC().Add(-1 * time.Hour)
		to := time.Now().UTC()
		if fs := paramString(params, "from"); fs != "" {
			if ft, err := time.Parse(time.RFC3339, fs); err == nil {
				from = ft
			}
		}
		if ts := paramString(params, "to"); ts != "" {
			if tt, err := time.Parse(time.RFC3339, ts); err == nil {
				to = tt
			}
		}
		v, err := s.svc.QueryReadings(r.Context(), metric, from, to)
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return v, nil, http.StatusOK
	case "get_forecast", "get_weather_forecast":
		v, err := s.svc.ForecastForTarget(r.Context(), target)
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return v, nil, http.StatusOK
	case "get_weather_current":
		v, err := s.svc.WeatherCurrent(r.Context(), target)
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return v, nil, http.StatusOK
	case "get_weather_alerts":
		v, err := s.svc.WeatherAlerts(r.Context(), target)
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return v, nil, http.StatusOK
	case "get_air_quality_current":
		v, err := s.svc.AirQualityCurrent(r.Context(), target)
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return v, nil, http.StatusOK
	case "get_air_quality_forecast":
		v, err := s.svc.AirQualityForecast(r.Context(), target)
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return v, nil, http.StatusOK
	case "get_climate_snapshot":
		v, err := s.svc.ClimateSnapshotForTarget(r.Context(), target)
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return v, nil, http.StatusOK
	case "get_data_gaps":
		v, err := s.svc.DataGaps(r.Context())
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return v, nil, http.StatusOK
	case "get_audit_events":
		from := time.Now().UTC().Add(-24 * time.Hour)
		to := time.Now().UTC()
		eventType := paramString(params, "type")
		v, err := s.svc.AuditEvents(r.Context(), from, to, eventType)
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return v, nil, http.StatusOK
	case "get_metrics":
		return s.svc.MetricsSnapshot(), nil, http.StatusOK

	// Phase C context methods.
	case "get_astronomy":
		v, err := s.svc.AstronomyContext(r.Context(), target)
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return v, nil, http.StatusOK
	case "get_wildfire_context":
		v, err := s.svc.WildfireContext(r.Context(), target)
		if err != nil {
			return nil, rpcErr(-32001, "no wildfire data available"), http.StatusNotFound
		}
		return v, nil, http.StatusOK
	case "get_pollen":
		v, err := s.svc.PollenContext(r.Context(), target)
		if err != nil {
			return nil, rpcErr(-32001, "no pollen data available"), http.StatusNotFound
		}
		return v, nil, http.StatusOK
	case "get_uv":
		v, err := s.svc.UVContext(r.Context(), target)
		if err != nil {
			return nil, rpcErr(-32001, "no UV data available"), http.StatusNotFound
		}
		return v, nil, http.StatusOK
	case "get_neighborhood_aq":
		v, err := s.svc.PurpleAirAQ(r.Context(), target)
		if err != nil {
			return nil, rpcErr(-32001, "no PurpleAir data available"), http.StatusNotFound
		}
		return v, nil, http.StatusOK
	case "list_provider_licenses":
		v, err := s.svc.SourceLicenses(r.Context())
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return v, nil, http.StatusOK
	case "list_consent_grants":
		v, err := s.svc.ConsentGrants(r.Context())
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return v, nil, http.StatusOK

	// Command plane (D-1).
	case "submit_command":
		capability := paramString(params, "capability")
		if capability == "" {
			return nil, rpcErr(-32602, "capability required"), http.StatusBadRequest
		}
		cmdTarget := paramString(params, "target")
		if cmdTarget == "" {
			cmdTarget = s.cfg.DefaultTargetID()
		}
		if methodUsesTarget("submit_command") {
			if err := s.authz.AuthorizeTarget(r, cmdTarget); err != nil {
				return nil, rpcErr(-32003, auth.Message(err)), auth.StatusCode(err)
			}
		}
		principal, _ := auth.PrincipalFromContext(r.Context())
		cmd := contracts.Command{
			CommandID:  mcpCommandID(),
			TargetID:   cmdTarget,
			Capability: capability,
			Actor:      contracts.CommandActor{Kind: "agent", ID: principal.Name},
		}
		if devID := paramString(params, "device_id"); devID != "" {
			cmd.DeviceID = devID
		}
		if args, ok := params["arguments"].(map[string]any); ok {
			cmd.Arguments = args
		}
		stored, err := s.svc.SubmitCommand(r.Context(), cmd)
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusBadRequest
		}
		return stored, nil, http.StatusOK
	case "get_command":
		commandID := paramString(params, "command_id")
		if commandID == "" {
			return nil, rpcErr(-32602, "command_id required"), http.StatusBadRequest
		}
		cmd, result, err := s.svc.GetCommand(r.Context(), commandID)
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusNotFound
		}
		return map[string]any{"command": cmd, "result": result}, nil, http.StatusOK
	case "list_commands":
		cmdTarget := paramString(params, "target")
		if cmdTarget == "" {
			cmdTarget = s.cfg.DefaultTargetID()
		}
		if err := s.authz.AuthorizeTarget(r, cmdTarget); err != nil {
			return nil, rpcErr(-32003, auth.Message(err)), auth.StatusCode(err)
		}
		cmds, err := s.svc.ListCommands(r.Context(), cmdTarget, 50)
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return cmds, nil, http.StatusOK
	case "update_command_status":
		commandID := paramString(params, "command_id")
		if commandID == "" {
			return nil, rpcErr(-32602, "command_id required"), http.StatusBadRequest
		}
		status := paramString(params, "status")
		if status == "" {
			return nil, rpcErr(-32602, "status required"), http.StatusBadRequest
		}
		observedEffect, _ := params["observed_effect"].(string)
		errMsg, _ := params["error"].(string)
		cmd, result, err := s.svc.UpdateCommandStatus(r.Context(), commandID,
			contracts.CommandStatus(status), observedEffect, errMsg)
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusBadRequest
		}
		return map[string]any{"command": cmd, "result": result}, nil, http.StatusOK

	default:
		return nil, rpcErr(-32601, "method not found"), http.StatusNotFound
	}
}

// methodUsesTarget returns true for MCP methods that read data for a specific
// monitoring target and therefore require per-target access control (X-5).
func methodUsesTarget(method string) bool {
	switch method {
	case "get_forecast", "get_weather_forecast",
		"get_weather_current", "get_weather_alerts",
		"get_air_quality_current", "get_air_quality_forecast",
		"get_climate_snapshot",
		"get_astronomy", "get_wildfire_context",
		"get_pollen", "get_uv", "get_neighborhood_aq":
		return true
	}
	return false
}

func methodScopes(method string) []string {
	switch method {
	case "list_capabilities", "list_targets", "get_station_health",
		"get_latest_readings", "query_readings", "get_data_gaps",
		"get_weather_current", "get_weather_alerts", "get_air_quality_current",
		"get_climate_snapshot", "get_astronomy", "get_wildfire_context",
		"get_pollen", "get_uv", "get_neighborhood_aq":
		return []string{auth.ScopeReadTelemetry}
	case "get_forecast", "get_weather_forecast", "get_air_quality_forecast":
		return []string{auth.ScopeReadForecast}
	case "get_audit_events":
		return []string{auth.ScopeReadAudit}
	case "get_metrics", "list_provider_licenses", "list_consent_grants":
		return []string{auth.ScopeAdminConfig}
	case "submit_command", "get_command", "list_commands", "update_command_status":
		return []string{auth.ScopeWriteCommands}
	default:
		return nil
	}
}

func (s *Server) instrument(surface, name string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.svc.RecordRequestMetric(surface, name, recorder.status, time.Since(startedAt))
	})
}

func rpcErr(code int, msg string) map[string]any {
	return map[string]any{"code": code, "message": msg}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func paramString(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}

func mcpCommandID() string {
	return fmt.Sprintf("cmd_%d", time.Now().UnixNano())
}
