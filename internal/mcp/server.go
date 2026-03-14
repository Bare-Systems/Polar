package mcp

import (
	"encoding/json"
	"net/http"
	"time"

	"polar/internal/auth"
	"polar/internal/config"
	"polar/internal/core"
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

	switch method {
	case "list_capabilities":
		return s.svc.Capabilities(), nil, http.StatusOK
	case "get_station_health":
		return s.svc.StationHealth(), nil, http.StatusOK
	case "get_latest_readings":
		v, err := s.svc.LatestReadings(r.Context())
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return v, nil, http.StatusOK
	case "query_readings":
		metric, _ := params["metric"].(string)
		if metric == "" {
			return nil, rpcErr(-32602, "metric required"), http.StatusBadRequest
		}
		from := time.Now().UTC().Add(-1 * time.Hour)
		to := time.Now().UTC()
		if fs, ok := params["from"].(string); ok && fs != "" {
			if ft, err := time.Parse(time.RFC3339, fs); err == nil {
				from = ft
			}
		}
		if ts, ok := params["to"].(string); ok && ts != "" {
			if tt, err := time.Parse(time.RFC3339, ts); err == nil {
				to = tt
			}
		}
		v, err := s.svc.QueryReadings(r.Context(), metric, from, to)
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return v, nil, http.StatusOK
	case "get_forecast":
		v, err := s.svc.LatestForecast(r.Context())
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
		eventType := ""
		if v, ok := params["type"].(string); ok {
			eventType = v
		}
		v, err := s.svc.AuditEvents(r.Context(), from, to, eventType)
		if err != nil {
			return nil, rpcErr(-32001, err.Error()), http.StatusInternalServerError
		}
		return v, nil, http.StatusOK
	case "get_metrics":
		return s.svc.MetricsSnapshot(), nil, http.StatusOK
	default:
		return nil, rpcErr(-32601, "method not found"), http.StatusNotFound
	}
}

func methodScopes(method string) []string {
	switch method {
	case "list_capabilities", "get_station_health", "get_latest_readings", "query_readings", "get_data_gaps":
		return []string{auth.ScopeReadTelemetry}
	case "get_forecast":
		return []string{auth.ScopeReadForecast}
	case "get_audit_events":
		return []string{auth.ScopeReadAudit}
	case "get_metrics":
		return []string{auth.ScopeAdminConfig}
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
