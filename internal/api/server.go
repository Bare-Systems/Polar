package api

import (
	"encoding/json"
	"errors"
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

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func NewServer(cfg config.Config, svc *core.Service, authz *auth.Auth) *Server {
	return &Server{cfg: cfg, svc: svc, authz: authz}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/healthz", s.instrument("rest", "/healthz", http.HandlerFunc(s.health)))
	mux.Handle("/readyz", s.instrument("rest", "/readyz", http.HandlerFunc(s.ready)))
	mux.Handle("/v1/capabilities", s.instrument("rest", "/v1/capabilities", s.authz.Require(http.HandlerFunc(s.capabilities), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/station/health", s.instrument("rest", "/v1/station/health", s.authz.Require(http.HandlerFunc(s.stationHealth), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/readings/latest", s.instrument("rest", "/v1/readings/latest", s.authz.Require(http.HandlerFunc(s.readingsLatest), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/readings", s.instrument("rest", "/v1/readings", s.authz.Require(http.HandlerFunc(s.queryReadings), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/forecast/latest", s.instrument("rest", "/v1/forecast/latest", s.authz.Require(http.HandlerFunc(s.forecastLatest), auth.ScopeReadForecast)))
	mux.Handle("/v1/forecast", s.instrument("rest", "/v1/forecast", s.authz.Require(http.HandlerFunc(s.forecastLatest), auth.ScopeReadForecast)))
	mux.Handle("/v1/diagnostics/data-gaps", s.instrument("rest", "/v1/diagnostics/data-gaps", s.authz.Require(http.HandlerFunc(s.dataGaps), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/audit/events", s.instrument("rest", "/v1/audit/events", s.authz.Require(http.HandlerFunc(s.auditEvents), auth.ScopeReadAudit)))
	mux.Handle("/v1/metrics", s.instrument("rest", "/v1/metrics", s.authz.Require(http.HandlerFunc(s.metrics), auth.ScopeAdminConfig)))
	mux.Handle("/v1/climate/snapshot", s.instrument("rest", "/v1/climate/snapshot", s.authz.Require(http.HandlerFunc(s.climateSnapshot), auth.ScopeReadTelemetry)))
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
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, readings)
}

func (s *Server) queryReadings(w http.ResponseWriter, r *http.Request) {
	metric := r.URL.Query().Get("metric")
	if metric == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "metric is required"})
		return
	}
	resolution, err := parseResolution(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	from, to, err := parseRange(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	readings, err := s.svc.QueryReadingsAtResolution(r.Context(), metric, from, to, resolution)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, readings)
}

func (s *Server) forecastLatest(w http.ResponseWriter, r *http.Request) {
	forecast, err := s.svc.LatestForecast(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, forecast)
}

func (s *Server) dataGaps(w http.ResponseWriter, r *http.Request) {
	gaps, err := s.svc.DataGaps(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, gaps)
}

func (s *Server) auditEvents(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseRange(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	eventType := r.URL.Query().Get("type")
	events, err := s.svc.AuditEvents(r.Context(), from, to, eventType)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) climateSnapshot(w http.ResponseWriter, r *http.Request) {
	snap, err := s.svc.ClimateSnapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.MetricsSnapshot())
}

func (s *Server) instrument(surface, name string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.svc.RecordRequestMetric(surface, name, recorder.status, time.Since(startedAt))
	})
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
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}
