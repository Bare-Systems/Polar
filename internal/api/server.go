package api

import (
	"encoding/json"
	"errors"
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

func NewServer(cfg config.Config, svc *core.Service, authz *auth.Auth) *Server {
	return &Server{cfg: cfg, svc: svc, authz: authz}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.ready)
	mux.Handle("/v1/capabilities", s.authz.Middleware(http.HandlerFunc(s.capabilities)))
	mux.Handle("/v1/station/health", s.authz.Middleware(http.HandlerFunc(s.stationHealth)))
	mux.Handle("/v1/readings/latest", s.authz.Middleware(http.HandlerFunc(s.readingsLatest)))
	mux.Handle("/v1/readings", s.authz.Middleware(http.HandlerFunc(s.queryReadings)))
	mux.Handle("/v1/forecast/latest", s.authz.Middleware(http.HandlerFunc(s.forecastLatest)))
	mux.Handle("/v1/forecast", s.authz.Middleware(http.HandlerFunc(s.forecastLatest)))
	mux.Handle("/v1/diagnostics/data-gaps", s.authz.Middleware(http.HandlerFunc(s.dataGaps)))
	mux.Handle("/v1/audit/events", s.authz.Middleware(http.HandlerFunc(s.auditEvents)))
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	ready := s.svc.Readiness()
	code := http.StatusOK
	if !ready.Ready {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, ready)
}

func (s *Server) capabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.Capabilities())
}

func (s *Server) stationHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.StationHealth())
}

func (s *Server) readingsLatest(w http.ResponseWriter, r *http.Request) {
	readings, err := s.svc.LatestReadings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, readings)
}

func (s *Server) queryReadings(w http.ResponseWriter, r *http.Request) {
	metric := r.URL.Query().Get("metric")
	if metric == "" {
		writeErr(w, http.StatusBadRequest, "INVALID_PARAM", "metric is required", false)
		return
	}
	if contracts.UnitFor(metric) == "" {
		writeErr(w, http.StatusBadRequest, "UNKNOWN_METRIC", "metric not in supported catalog: "+metric, false)
		return
	}
	resolution, err := parseResolution(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_PARAM", err.Error(), false)
		return
	}
	from, to, err := parseRange(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_PARAM", err.Error(), false)
		return
	}
	if !to.After(from) {
		writeErr(w, http.StatusBadRequest, "INVALID_RANGE", "to must be after from", false)
		return
	}
	readings, err := s.svc.QueryReadingsAtResolution(r.Context(), metric, from, to, resolution)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, readings)
}

func (s *Server) forecastLatest(w http.ResponseWriter, r *http.Request) {
	forecast, err := s.svc.LatestForecast(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, forecast)
}

func (s *Server) dataGaps(w http.ResponseWriter, r *http.Request) {
	gaps, err := s.svc.DataGaps(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, gaps)
}

func (s *Server) auditEvents(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseRange(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_PARAM", err.Error(), false)
		return
	}
	if !to.After(from) {
		writeErr(w, http.StatusBadRequest, "INVALID_RANGE", "to must be after from", false)
		return
	}
	eventType := r.URL.Query().Get("type")
	events, err := s.svc.AuditEvents(r.Context(), from, to, eventType)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func parseRange(r *http.Request) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	if fromStr == "" {
		fromStr = now.Add(-1 * time.Hour).Format(time.RFC3339)
	}
	if toStr == "" {
		toStr = now.Format(time.RFC3339)
	}
	from, err := time.Parse(time.RFC3339, fromStr)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	to, err := time.Parse(time.RFC3339, toStr)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return from, to, nil
}

func parseResolution(r *http.Request) (time.Duration, error) {
	raw := r.URL.Query().Get("resolution")
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, errors.New("resolution must be > 0")
	}
	return d, nil
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Polar-Schema", contracts.SchemaVersion)
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeErr(w http.ResponseWriter, code int, errCode, message string, retryable bool) {
	writeJSON(w, code, contracts.APIError{
		Code:      errCode,
		Message:   message,
		Retryable: retryable,
	})
}
