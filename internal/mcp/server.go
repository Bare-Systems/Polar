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

func NewServer(cfg config.Config, svc *core.Service, authz *auth.Auth) *Server {
	return &Server{cfg: cfg, svc: svc, authz: authz}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.Handle("/mcp", s.authz.Middleware(http.HandlerFunc(s.rpc)))
	return mux
}

func (s *Server) rpc(w http.ResponseWriter, r *http.Request) {
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: nil, Error: map[string]any{"code": -32700, "message": "invalid json"}})
		return
	}

	result, errObj := s.dispatch(r, req.Method, req.Params)
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if errObj != nil {
		resp.Error = errObj
	} else {
		resp.Result = result
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) dispatch(r *http.Request, method string, params map[string]any) (any, any) {
	switch method {
	case "list_capabilities":
		return s.svc.Capabilities(), nil
	case "get_station_health":
		return s.svc.StationHealth(), nil
	case "get_latest_readings":
		v, err := s.svc.LatestReadings(r.Context())
		if err != nil {
			return nil, rpcErr(-32001, err.Error())
		}
		return v, nil
	case "query_readings":
		metric, _ := params["metric"].(string)
		if metric == "" {
			return nil, rpcErr(-32602, "metric required")
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
			return nil, rpcErr(-32001, err.Error())
		}
		return v, nil
	case "get_forecast":
		v, err := s.svc.LatestForecast(r.Context())
		if err != nil {
			return nil, rpcErr(-32001, err.Error())
		}
		return v, nil
	case "get_data_gaps":
		v, err := s.svc.DataGaps(r.Context())
		if err != nil {
			return nil, rpcErr(-32001, err.Error())
		}
		return v, nil
	case "get_audit_events":
		from := time.Now().UTC().Add(-24 * time.Hour)
		to := time.Now().UTC()
		eventType := ""
		if v, ok := params["type"].(string); ok {
			eventType = v
		}
		v, err := s.svc.AuditEvents(r.Context(), from, to, eventType)
		if err != nil {
			return nil, rpcErr(-32001, err.Error())
		}
		return v, nil
	default:
		return nil, rpcErr(-32601, "method not found")
	}
}

func rpcErr(code int, msg string) map[string]any {
	return map[string]any{"code": code, "message": msg}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
