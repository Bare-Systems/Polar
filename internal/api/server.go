package api

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"polar/internal/auth"
	"polar/internal/config"
	"polar/internal/core"
	"polar/pkg/contracts"
)

type Server struct {
	cfg      config.Config
	svc      *core.Service
	authz    *auth.Auth
	upgrader websocket.Upgrader
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func NewServer(cfg config.Config, svc *core.Service, authz *auth.Auth) *Server {
	return &Server{
		cfg:   cfg,
		svc:   svc,
		authz: authz,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool { return true },
		},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Health / readiness (no auth).
	mux.Handle("/healthz", s.instrument("rest", "/healthz", http.HandlerFunc(s.health)))
	mux.Handle("/readyz", s.instrument("rest", "/readyz", http.HandlerFunc(s.ready)))

	// Telemetry endpoints.
	mux.Handle("/v1/capabilities", s.instrument("rest", "/v1/capabilities", s.authz.Require(http.HandlerFunc(s.capabilities), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/station/health", s.instrument("rest", "/v1/station/health", s.authz.Require(http.HandlerFunc(s.stationHealth), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/readings/latest", s.instrument("rest", "/v1/readings/latest", s.authz.Require(http.HandlerFunc(s.readingsLatest), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/readings", s.instrument("rest", "/v1/readings", s.authz.Require(http.HandlerFunc(s.queryReadings), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/targets", s.instrument("rest", "/v1/targets", s.authz.Require(http.HandlerFunc(s.targets), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/diagnostics/data-gaps", s.instrument("rest", "/v1/diagnostics/data-gaps", s.authz.Require(http.HandlerFunc(s.dataGaps), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/climate/snapshot", s.instrument("rest", "/v1/climate/snapshot", s.authz.Require(http.HandlerFunc(s.climateSnapshot), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/live", s.instrument("rest", "/v1/live", s.authz.Require(http.HandlerFunc(s.live), auth.ScopeReadTelemetry)))

	// Weather endpoints.
	mux.Handle("/v1/weather/current", s.instrument("rest", "/v1/weather/current", s.authz.Require(http.HandlerFunc(s.weatherCurrent), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/weather/alerts", s.instrument("rest", "/v1/weather/alerts", s.authz.Require(http.HandlerFunc(s.weatherAlerts), auth.ScopeReadTelemetry)))

	// Forecast endpoints.
	mux.Handle("/v1/forecast/latest", s.instrument("rest", "/v1/forecast/latest", s.authz.Require(http.HandlerFunc(s.forecastLatest), auth.ScopeReadForecast)))
	mux.Handle("/v1/forecast", s.instrument("rest", "/v1/forecast", s.authz.Require(http.HandlerFunc(s.forecastLatest), auth.ScopeReadForecast)))
	mux.Handle("/v1/weather/forecast", s.instrument("rest", "/v1/weather/forecast", s.authz.Require(http.HandlerFunc(s.forecastLatest), auth.ScopeReadForecast)))

	// Air quality endpoints.
	mux.Handle("/v1/air-quality/current", s.instrument("rest", "/v1/air-quality/current", s.authz.Require(http.HandlerFunc(s.airQualityCurrent), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/air-quality/forecast", s.instrument("rest", "/v1/air-quality/forecast", s.authz.Require(http.HandlerFunc(s.airQualityForecast), auth.ScopeReadForecast)))

	// Phase C context endpoints.
	mux.Handle("/v1/astronomy/today", s.instrument("rest", "/v1/astronomy/today", s.authz.Require(http.HandlerFunc(s.astronomyToday), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/wildfire/current", s.instrument("rest", "/v1/wildfire/current", s.authz.Require(http.HandlerFunc(s.wildfireCurrent), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/pollen/current", s.instrument("rest", "/v1/pollen/current", s.authz.Require(http.HandlerFunc(s.pollenCurrent), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/uv/current", s.instrument("rest", "/v1/uv/current", s.authz.Require(http.HandlerFunc(s.uvCurrent), auth.ScopeReadTelemetry)))
	mux.Handle("/v1/air-quality/neighborhood", s.instrument("rest", "/v1/air-quality/neighborhood", s.authz.Require(http.HandlerFunc(s.purpleAirCurrent), auth.ScopeReadTelemetry)))

	// Admin / audit endpoints.
	mux.Handle("/v1/audit/events", s.instrument("rest", "/v1/audit/events", s.authz.Require(http.HandlerFunc(s.auditEvents), auth.ScopeReadAudit)))
	mux.Handle("/v1/metrics", s.instrument("rest", "/v1/metrics", s.authz.Require(http.HandlerFunc(s.metrics), auth.ScopeAdminConfig)))
	mux.Handle("/v1/providers/licenses", s.instrument("rest", "/v1/providers/licenses", s.authz.Require(http.HandlerFunc(s.providerLicenses), auth.ScopeAdminConfig)))
	mux.Handle("/v1/consent/grants", s.instrument("rest", "/v1/consent/grants", s.authz.Require(http.HandlerFunc(s.consentGrants), auth.ScopeAdminConfig)))

	// Command plane (D-1).
	mux.Handle("/v1/commands", s.instrument("rest", "/v1/commands", s.authz.Require(http.HandlerFunc(s.commands), auth.ScopeWriteCommands)))
	// /v1/commands/{id} (GET) and /v1/commands/{id}/status (PATCH) share the prefix handler.
	mux.Handle("/v1/commands/", s.instrument("rest", "/v1/commands/{id}", s.authz.Require(http.HandlerFunc(s.commandByID), auth.ScopeWriteCommands)))

	// Auth — token minting (A-6).
	mux.Handle("/v1/auth/token", s.instrument("rest", "/v1/auth/token", s.authz.Require(http.HandlerFunc(s.mintToken), auth.ScopeAdminConfig)))

	return mux
}

// --- Health ---

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

// --- Telemetry ---

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

func (s *Server) targets(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.Targets())
}

func (s *Server) dataGaps(w http.ResponseWriter, r *http.Request) {
	report, err := s.svc.DataGaps(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) climateSnapshot(w http.ResponseWriter, r *http.Request) {
	targetID := s.resolveTarget(r)
	if !s.checkTargetAccess(w, r, targetID) {
		return
	}
	snap, err := s.svc.ClimateSnapshotForTarget(r.Context(), targetID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// --- Weather ---

func (s *Server) forecastLatest(w http.ResponseWriter, r *http.Request) {
	targetID := s.resolveTarget(r)
	if !s.checkTargetAccess(w, r, targetID) {
		return
	}
	forecast, err := s.svc.ForecastForTarget(r.Context(), targetID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, forecast)
}

func (s *Server) weatherCurrent(w http.ResponseWriter, r *http.Request) {
	targetID := s.resolveTarget(r)
	if !s.checkTargetAccess(w, r, targetID) {
		return
	}
	current, err := s.svc.WeatherCurrent(r.Context(), targetID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, current)
}

func (s *Server) weatherAlerts(w http.ResponseWriter, r *http.Request) {
	targetID := s.resolveTarget(r)
	if !s.checkTargetAccess(w, r, targetID) {
		return
	}
	alerts, err := s.svc.WeatherAlerts(r.Context(), targetID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, alerts)
}

// --- Air quality ---

func (s *Server) airQualityCurrent(w http.ResponseWriter, r *http.Request) {
	targetID := s.resolveTarget(r)
	if !s.checkTargetAccess(w, r, targetID) {
		return
	}
	current, err := s.svc.AirQualityCurrent(r.Context(), targetID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, current)
}

func (s *Server) airQualityForecast(w http.ResponseWriter, r *http.Request) {
	targetID := s.resolveTarget(r)
	if !s.checkTargetAccess(w, r, targetID) {
		return
	}
	forecast, err := s.svc.AirQualityForecast(r.Context(), targetID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, forecast)
}

// --- Phase C context endpoints ---

func (s *Server) astronomyToday(w http.ResponseWriter, r *http.Request) {
	targetID := s.resolveTarget(r)
	if !s.checkTargetAccess(w, r, targetID) {
		return
	}
	astro, err := s.svc.AstronomyContext(r.Context(), targetID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, astro)
}

func (s *Server) wildfireCurrent(w http.ResponseWriter, r *http.Request) {
	targetID := s.resolveTarget(r)
	if !s.checkTargetAccess(w, r, targetID) {
		return
	}
	wf, err := s.svc.WildfireContext(r.Context(), targetID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no wildfire data available"})
		return
	}
	writeJSON(w, http.StatusOK, wf)
}

func (s *Server) pollenCurrent(w http.ResponseWriter, r *http.Request) {
	targetID := s.resolveTarget(r)
	if !s.checkTargetAccess(w, r, targetID) {
		return
	}
	pollen, err := s.svc.PollenContext(r.Context(), targetID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no pollen data available"})
		return
	}
	writeJSON(w, http.StatusOK, pollen)
}

func (s *Server) uvCurrent(w http.ResponseWriter, r *http.Request) {
	targetID := s.resolveTarget(r)
	if !s.checkTargetAccess(w, r, targetID) {
		return
	}
	uv, err := s.svc.UVContext(r.Context(), targetID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no UV data available"})
		return
	}
	writeJSON(w, http.StatusOK, uv)
}

func (s *Server) purpleAirCurrent(w http.ResponseWriter, r *http.Request) {
	targetID := s.resolveTarget(r)
	if !s.checkTargetAccess(w, r, targetID) {
		return
	}
	pa, err := s.svc.PurpleAirAQ(r.Context(), targetID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no PurpleAir data available"})
		return
	}
	writeJSON(w, http.StatusOK, pa)
}

// --- Admin ---

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

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.MetricsSnapshot())
}

// providerLicenses returns all registered provider license records (A-5).
func (s *Server) providerLicenses(w http.ResponseWriter, r *http.Request) {
	licenses, err := s.svc.SourceLicenses(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, licenses)
}

func (s *Server) consentGrants(w http.ResponseWriter, r *http.Request) {
	grants, err := s.svc.ConsentGrants(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, grants)
}

// mintToken creates a short-lived scoped token (A-6).
// POST /v1/auth/token  body: {"name":"…","scopes":["read:telemetry"],"ttl_seconds":3600}
func (s *Server) mintToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST required"})
		return
	}
	var req struct {
		Name       string   `json:"name"`
		Scopes     []string `json:"scopes"`
		TTLSeconds int      `json:"ttl_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
		return
	}
	if len(req.Scopes) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "scopes is required"})
		return
	}
	ttl := req.TTLSeconds
	if ttl <= 0 {
		ttl = 3600
	}
	expiresAt := time.Now().UTC().Add(time.Duration(ttl) * time.Second)
	// Generate a pseudo-random token value using current timestamp + name hash.
	// In production, use crypto/rand for stronger entropy.
	tokenValue := mintTokenValue(req.Name, expiresAt)
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      tokenValue,
		"name":       req.Name,
		"scopes":     req.Scopes,
		"expires_at": expiresAt,
		"note":       "This token is not persisted. Configure it in auth.tokens to make it durable.",
	})
}

// --- Command plane (D-1) ---

// commands handles POST /v1/commands (submit) and GET /v1/commands?target= (list).
func (s *Server) commands(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.submitCommand(w, r)
	case http.MethodGet:
		s.listCommands(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST or GET required"})
	}
}

func (s *Server) submitCommand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TargetID       string         `json:"target_id"`
		DeviceID       string         `json:"device_id,omitempty"`
		Capability     string         `json:"capability"`
		Arguments      map[string]any `json:"arguments,omitempty"`
		IdempotencyKey string         `json:"idempotency_key,omitempty"`
		TTLSeconds     int            `json:"ttl_seconds,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if req.TargetID == "" {
		req.TargetID = s.cfg.DefaultTargetID()
	}
	if !s.checkTargetAccess(w, r, req.TargetID) {
		return
	}

	// Identify the actor from the authenticated principal.
	principal, _ := auth.PrincipalFromContext(r.Context())
	cmd := contracts.Command{
		CommandID:      newCommandID(),
		TargetID:       req.TargetID,
		DeviceID:       req.DeviceID,
		Capability:     req.Capability,
		Arguments:      req.Arguments,
		IdempotencyKey: req.IdempotencyKey,
		Actor:          contracts.CommandActor{Kind: "user", ID: principal.Name},
	}
	if req.TTLSeconds < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "ttl_seconds must be positive"})
		return
	}
	if req.TTLSeconds > 0 {
		exp := time.Now().UTC().Add(time.Duration(req.TTLSeconds) * time.Second)
		cmd.ExpiresAt = &exp
	}

	stored, err := s.svc.SubmitCommand(r.Context(), cmd)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, stored)
}

func (s *Server) listCommands(w http.ResponseWriter, r *http.Request) {
	targetID := s.resolveTarget(r)
	if !s.checkTargetAccess(w, r, targetID) {
		return
	}
	cmds, err := s.svc.ListCommands(r.Context(), targetID, 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, cmds)
}

// commandByID handles:
//   GET  /v1/commands/{id}         — fetch command + result
//   PATCH /v1/commands/{id}/status — advance command lifecycle
func (s *Server) commandByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/commands/")
	// Detect /status sub-resource.
	if strings.HasSuffix(rest, "/status") {
		commandID := strings.TrimSuffix(rest, "/status")
		if r.Method != http.MethodPatch {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "PATCH required"})
			return
		}
		s.patchCommandStatus(w, r, commandID)
		return
	}

	commandID := rest
	if commandID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "command_id required"})
		return
	}
	cmd, result, err := s.svc.GetCommand(r.Context(), commandID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "command not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !s.checkTargetAccess(w, r, cmd.TargetID) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"command": cmd, "result": result})
}

// patchCommandStatus handles PATCH /v1/commands/{id}/status.
func (s *Server) patchCommandStatus(w http.ResponseWriter, r *http.Request, commandID string) {
	if commandID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "command_id required"})
		return
	}
	var req struct {
		Status         string `json:"status"`
		ObservedEffect string `json:"observed_effect,omitempty"`
		Error          string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if req.Status == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "status is required"})
		return
	}

	// Verify target access on the existing command.
	existing, _, err := s.svc.GetCommand(r.Context(), commandID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "command not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !s.checkTargetAccess(w, r, existing.TargetID) {
		return
	}

	cmd, result, err := s.svc.UpdateCommandStatus(r.Context(), commandID,
		contracts.CommandStatus(req.Status), req.ObservedEffect, req.Error)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"command": cmd, "result": result})
}

func newCommandID() string {
	return fmt.Sprintf("cmd_%s", strings.ReplaceAll(fmt.Sprintf("%d", time.Now().UnixNano()), "-", ""))
}

// mintTokenValue creates a deterministic token string from name + expiry.
// In production this should use crypto/rand.
func mintTokenValue(name string, expiresAt time.Time) string {
	return fmt.Sprintf("pt_%x_%x", []byte(name), expiresAt.UnixNano())
}

// --- Live WebSocket feed (A-3: event type filtering) ---

func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	targetID := s.resolveTarget(r)
	if !s.checkTargetAccess(w, r, targetID) {
		return
	}
	ch, err := s.svc.Subscribe(targetID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	defer s.svc.Unsubscribe(targetID, ch)

	// Parse optional event type filter: ?types=reading_updated,weather_updated
	var typeFilter map[string]struct{}
	if raw := r.URL.Query().Get("types"); raw != "" {
		typeFilter = make(map[string]struct{})
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				typeFilter[t] = struct{}{}
			}
		}
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	initial, err := s.svc.ClimateSnapshotForTarget(r.Context(), targetID)
	if err == nil {
		if err := conn.WriteJSON(map[string]any{"type": "snapshot_full", "snapshot": initial}); err != nil {
			return
		}
	}

	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})

	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-done:
			return
		case update := <-ch:
			// Apply event type filter if provided.
			if typeFilter != nil {
				if _, ok := typeFilter[update.Type]; !ok {
					continue
				}
			}
			if err := conn.WriteJSON(update); err != nil {
				return
			}
		case <-pingTicker.C:
			if err := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}
}

// --- Target resolution and access control (X-5) ---

// resolveTarget returns the target from the query param, falling back to the
// configured default target when the param is absent or empty.
func (s *Server) resolveTarget(r *http.Request) string {
	if t := r.URL.Query().Get("target"); t != "" {
		return t
	}
	return s.cfg.DefaultTargetID()
}

// checkTargetAccess verifies the authenticated principal may access targetID.
// It writes a JSON error response and returns false if access is denied.
func (s *Server) checkTargetAccess(w http.ResponseWriter, r *http.Request, targetID string) bool {
	if err := s.authz.AuthorizeTarget(r, targetID); err != nil {
		writeJSON(w, auth.StatusCode(err), map[string]any{"error": auth.Message(err)})
		return false
	}
	return true
}

// --- Instrumentation and helpers ---

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

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}
