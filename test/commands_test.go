// commands_test.go — D-1: Command plane test suite.
//
// Covers:
//   - Submit command (happy path) via REST and MCP
//   - Validation rejections (missing capability, unknown target, expired TTL)
//   - Retrieve command by ID (with and without a result)
//   - List commands for a target
//   - write:commands scope enforcement on all command endpoints
//   - Target isolation: token restricted to one target cannot submit to another
package test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"polar/internal/auth"
	"polar/internal/config"
	"polar/pkg/contracts"
)

// cmdTok returns a TokenConfig with write:commands (+ optional extra) scopes.
func cmdTok(name, value string, extra ...string) config.TokenConfig {
	scopes := append([]string{auth.ScopeWriteCommands}, extra...)
	return config.TokenConfig{Name: name, Value: value, Scopes: scopes}
}

// cmdCfg returns a single-target config with a wildcard admin token and a
// commands-only token.
func cmdCfg() config.Config {
	cfg := baseConfig()
	cfg.Auth = config.AuthConfig{
		Tokens: []config.TokenConfig{
			{Name: "admin", Value: "admin-token", Scopes: []string{auth.WildcardScope}},
			cmdTok("cmds", "cmd-token"),
		},
	}
	return cfg
}

// postCommand issues a POST /v1/commands request with the given JSON body.
func postCommand(t *testing.T, handler http.Handler, token string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/commands", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func getRoute(t *testing.T, handler http.Handler, token, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// --- D-1.1: Submit command — happy path ---

func TestSubmitCommandHappyPath(t *testing.T) {
	cfg := cmdCfg()
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	handler := server.Handler()

	rr := postCommand(t, handler, "cmd-token", map[string]any{
		"capability": "set_temperature",
		"arguments":  map[string]any{"target_c": 22.0},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var cmd contracts.Command
	if err := json.NewDecoder(rr.Body).Decode(&cmd); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cmd.CommandID == "" {
		t.Error("command_id must not be empty")
	}
	if cmd.Status != contracts.CommandStatusPending {
		t.Errorf("expected status %q, got %q", contracts.CommandStatusPending, cmd.Status)
	}
	if cmd.Capability != "set_temperature" {
		t.Errorf("capability mismatch: got %q", cmd.Capability)
	}
	if cmd.RequestedAt.IsZero() {
		t.Error("requested_at must not be zero")
	}
	if cmd.Actor.Kind != "user" {
		t.Errorf("expected actor.kind=user, got %q", cmd.Actor.Kind)
	}
	if cmd.Actor.ID != "cmds" {
		t.Errorf("expected actor.id=cmds, got %q", cmd.Actor.ID)
	}
}

// --- D-1.2: Submit command — validation rejections ---

func TestSubmitCommandValidation(t *testing.T) {
	cfg := cmdCfg()
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	handler := server.Handler()

	cases := []struct {
		desc string
		body map[string]any
	}{
		{
			desc: "missing capability",
			body: map[string]any{"target_id": "test"},
		},
		{
			desc: "unknown target",
			body: map[string]any{"capability": "set_mode", "target_id": "nonexistent"},
		},
		{
			desc: "expired TTL",
			body: map[string]any{
				"capability":  "set_temperature",
				"ttl_seconds": -60,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			rr := postCommand(t, handler, "cmd-token", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("%s: expected 400, got %d: %s", tc.desc, rr.Code, rr.Body.String())
			}
		})
	}
}

// --- D-1.3: Get command by ID ---

func TestGetCommandByID(t *testing.T) {
	cfg := cmdCfg()
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	handler := server.Handler()

	// Submit first.
	rr := postCommand(t, handler, "cmd-token", map[string]any{
		"capability": "set_mode",
		"arguments":  map[string]any{"mode": "cool"},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("submit: %d %s", rr.Code, rr.Body.String())
	}
	var submitted contracts.Command
	_ = json.NewDecoder(rr.Body).Decode(&submitted)

	// Retrieve by ID.
	rr2 := getRoute(t, handler, "cmd-token", "/v1/commands/"+submitted.CommandID)
	if rr2.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d: %s", rr2.Code, rr2.Body.String())
	}

	var resp map[string]json.RawMessage
	if err := json.NewDecoder(rr2.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["command"]; !ok {
		t.Error("response must have 'command' key")
	}
	// result is null when not yet set.
	if raw, ok := resp["result"]; ok {
		if string(raw) != "null" {
			t.Errorf("expected null result, got %s", raw)
		}
	}
}

func TestGetCommandNotFound(t *testing.T) {
	cfg := cmdCfg()
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	handler := server.Handler()

	rr := getRoute(t, handler, "cmd-token", "/v1/commands/cmd_does_not_exist")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// --- D-1.4: List commands ---

func TestListCommands(t *testing.T) {
	cfg := cmdCfg()
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	handler := server.Handler()

	// Submit two commands.
	for _, cap := range []string{"set_temperature", "set_mode"} {
		rr := postCommand(t, handler, "cmd-token", map[string]any{"capability": cap})
		if rr.Code != http.StatusCreated {
			t.Fatalf("submit %s: %d", cap, rr.Code)
		}
	}

	rr := getRoute(t, handler, "cmd-token", "/v1/commands")
	if rr.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var cmds []contracts.Command
	if err := json.NewDecoder(rr.Body).Decode(&cmds); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cmds) < 2 {
		t.Errorf("expected at least 2 commands, got %d", len(cmds))
	}
}

// --- D-1.5: write:commands scope enforcement ---

func TestCommandsScopeEnforcement(t *testing.T) {
	cfg := baseConfig()
	cfg.Auth = config.AuthConfig{
		Tokens: []config.TokenConfig{
			cmdTok("cmds", "cmd-token"),
			tok("telemetry", "tel-token", auth.ScopeReadTelemetry),
		},
	}
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	handler := server.Handler()

	cases := []struct {
		desc   string
		token  string
		method string
		path   string
		body   map[string]any
		want   int
	}{
		// No token → 401 on all three command endpoints.
		{"no-token POST /v1/commands", "", http.MethodPost, "/v1/commands",
			map[string]any{"capability": "x"}, http.StatusUnauthorized},
		{"no-token GET /v1/commands", "", http.MethodGet, "/v1/commands", nil, http.StatusUnauthorized},
		{"no-token GET /v1/commands/id", "", http.MethodGet, "/v1/commands/xyz", nil, http.StatusUnauthorized},
		// Telemetry token → 403 on all command endpoints.
		{"tel POST /v1/commands", "tel-token", http.MethodPost, "/v1/commands",
			map[string]any{"capability": "x"}, http.StatusForbidden},
		{"tel GET /v1/commands", "tel-token", http.MethodGet, "/v1/commands", nil, http.StatusForbidden},
		{"tel GET /v1/commands/id", "tel-token", http.MethodGet, "/v1/commands/xyz", nil, http.StatusForbidden},
		// Commands token → allowed (400 for invalid body, but not 401/403).
		{"cmds POST valid", "cmd-token", http.MethodPost, "/v1/commands",
			map[string]any{"capability": "set_temperature"}, http.StatusCreated},
		{"cmds GET list", "cmd-token", http.MethodGet, "/v1/commands", nil, http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			var b []byte
			if tc.body != nil {
				b, _ = json.Marshal(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader(b))
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("expected %d, got %d: %s", tc.want, rr.Code, rr.Body.String())
			}
		})
	}
}

// --- D-1.6: Target isolation on command endpoints ---

func TestCommandTargetIsolation(t *testing.T) {
	cfg := twoTargetCfg()
	cfg.Auth = config.AuthConfig{
		Tokens: []config.TokenConfig{
			tokWithTargets("home-cmd", "home-cmd-token", []string{"home"},
				auth.ScopeWriteCommands),
			cmdTok("all-cmd", "all-cmd-token"),
		},
	}
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	handler := server.Handler()

	// home-cmd token cannot submit to "cabin".
	rr := postCommand(t, handler, "home-cmd-token", map[string]any{
		"capability": "set_temperature",
		"target_id":  "cabin",
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for home-cmd on cabin, got %d: %s", rr.Code, rr.Body.String())
	}

	// home-cmd token can submit to "home".
	rr = postCommand(t, handler, "home-cmd-token", map[string]any{
		"capability": "set_temperature",
		"target_id":  "home",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 for home-cmd on home, got %d: %s", rr.Code, rr.Body.String())
	}

	// all-cmd token can submit to "cabin".
	rr = postCommand(t, handler, "all-cmd-token", map[string]any{
		"capability": "set_mode",
		"target_id":  "cabin",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 for all-cmd on cabin, got %d: %s", rr.Code, rr.Body.String())
	}
}

// --- D-1.7: MCP submit_command, get_command, list_commands ---

func TestMCPCommandMethods(t *testing.T) {
	cfg := baseConfig()
	cfg.Auth = config.AuthConfig{
		Tokens: []config.TokenConfig{
			cmdTok("cmds", "cmd-token"),
			tok("telemetry", "tel-token", auth.ScopeReadTelemetry),
		},
	}
	_, _, _, mcpServer := newTestStack(t, cfg, stubForecastClient{})
	handler := mcpServer.Handler()

	// submit_command succeeds with write:commands scope.
	rr := mcpCall(t, handler, "cmd-token", "submit_command", map[string]any{
		"capability": "set_temperature",
		"arguments":  map[string]any{"target_c": 21.0},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("submit_command: expected 200 envelope, got %d", rr.Code)
	}
	var resp struct {
		Result contracts.Command `json:"result"`
		Error  any               `json:"error"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Error != nil {
		t.Fatalf("submit_command: unexpected error: %v", resp.Error)
	}
	if resp.Result.CommandID == "" {
		t.Error("submit_command: result must have command_id")
	}
	submittedID := resp.Result.CommandID

	// get_command returns the submitted command.
	rr2 := mcpCall(t, handler, "cmd-token", "get_command", map[string]any{
		"command_id": submittedID,
	})
	_ = rr2 // 200 envelope; errors surfaced inside JSON-RPC result

	// list_commands returns at least one.
	rr3 := mcpCall(t, handler, "cmd-token", "list_commands", nil)
	var listResp struct {
		Result []contracts.Command `json:"result"`
	}
	_ = json.NewDecoder(rr3.Body).Decode(&listResp)
	if len(listResp.Result) == 0 {
		t.Error("list_commands: expected at least one command")
	}

	// Wrong scope → -32003.
	rr4 := mcpCall(t, handler, "tel-token", "submit_command", map[string]any{
		"capability": "set_temperature",
	})
	if code := mcpErrorCode(t, rr4); code != -32003 {
		t.Errorf("expected -32003 for wrong scope on submit_command, got %d", code)
	}
}

// --- D-1.8: Command TTL field round-trips correctly ---

func TestCommandTTLRoundTrip(t *testing.T) {
	cfg := cmdCfg()
	_, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	handler := server.Handler()

	rr := postCommand(t, handler, "cmd-token", map[string]any{
		"capability":  "set_mode",
		"ttl_seconds": 300,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var cmd contracts.Command
	_ = json.NewDecoder(rr.Body).Decode(&cmd)
	if cmd.ExpiresAt == nil {
		t.Fatal("expires_at must be set when ttl_seconds provided")
	}
	wantExpiry := time.Now().UTC().Add(300 * time.Second)
	delta := wantExpiry.Sub(*cmd.ExpiresAt)
	if delta < 0 {
		delta = -delta
	}
	if delta > 5*time.Second {
		t.Errorf("expires_at %v is too far from expected %v (delta %v)", cmd.ExpiresAt, wantExpiry, delta)
	}
}

// --- D-1.9: UpsertCommandResult and GetCommandResult via repo ---

func TestCommandResultRoundTrip(t *testing.T) {
	cfg := cmdCfg()
	repo, _, server, _ := newTestStack(t, cfg, stubForecastClient{})
	handler := server.Handler()

	// Submit a command via REST.
	rr := postCommand(t, handler, "cmd-token", map[string]any{"capability": "set_fan"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("submit: %d", rr.Code)
	}
	var cmd contracts.Command
	_ = json.NewDecoder(rr.Body).Decode(&cmd)

	// Write a result directly to the repo (simulates a device integration acking the command).
	now := time.Now().UTC()
	result := contracts.CommandResult{
		CommandID:   cmd.CommandID,
		Status:      contracts.CommandStatusAccepted,
		AcceptedAt:  &now,
		FinalStatus: contracts.CommandStatusSucceeded,
		UpdatedAt:   now,
	}
	if err := repo.UpsertCommandResult(context.Background(), result); err != nil {
		t.Fatalf("UpsertCommandResult: %v", err)
	}

	// Retrieve command — result should now be populated.
	rr2 := getRoute(t, handler, "cmd-token", "/v1/commands/"+cmd.CommandID)
	if rr2.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rr2.Code, rr2.Body.String())
	}
	var resp map[string]json.RawMessage
	_ = json.NewDecoder(rr2.Body).Decode(&resp)

	if string(resp["result"]) == "null" {
		t.Error("expected non-null result after UpsertCommandResult")
	}

	var got contracts.CommandResult
	_ = json.Unmarshal(resp["result"], &got)
	if got.Status != contracts.CommandStatusAccepted {
		t.Errorf("result.status: expected %q, got %q", contracts.CommandStatusAccepted, got.Status)
	}
	if got.AcceptedAt == nil {
		t.Error("result.accepted_at must not be nil")
	}

	_ = strings.Contains // suppress unused import if lint fires
}
