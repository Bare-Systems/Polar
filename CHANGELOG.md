# Changelog

## Unreleased

- Disabled the legacy host-level `polar.service` during Blink deploys and hardened the stop step to kill any leftover `/home/admin/baresystems/runtime/polar/bin/polar` process before replacing the managed container. This keeps Polar on the Blink-managed `6703` contract and prevents it from reclaiming `:8080` on reboot.

### Phase A — Platform Tightening
- **A-1** Added nightly retention pruning for `outdoor_snapshots`, `weather_alerts`, `audit_events`, and legacy `forecasts` rows. Configurable via `POLAR_RETENTION_SNAPSHOT_DAYS`, `POLAR_RETENTION_ALERT_DAYS`, `POLAR_RETENTION_AUDIT_DAYS` (defaults: 90 / 30 / 90 days).
- **A-2** Fixed NOAA forecast parser: precipitation probability (`probabilityOfPrecipitation`) is now correctly mapped to `precip_probability_pct` (integer %) rather than misused as `precip_mm`. Added `wind_direction_deg` to `ForecastPoint` from NOAA cardinal strings (`cardinalToDeg`) and Open-Meteo `wind_direction_10m`. Added `precipitation_probability` field to Open-Meteo request.
- **A-3** Typed live-feed events: `publishSnapshot` is now `publishEvent(ctx, targetID, eventType)`. Schedulers emit specific event types (`reading_updated`, `weather_updated`, `air_quality_updated`). WebSocket handler supports `?types=` filter query param.
- **A-4** Added `deploy/postgres-provision.md` with role creation, database init, env var wiring, and rollback instructions for the Beelink homelab stack.
- **A-5** Added `source_licenses` table and `SourceLicense` contract type. Built-in license metadata for all active providers (NOAA, Open-Meteo, AirNow, NASA FIRMS, WeatherAPI, PurpleAir, Airthings, Shelly, SwitchBot) seeded at startup via `SeedSourceLicenses`. Exposed as `GET /v1/providers/licenses` and `list_provider_licenses` MCP method.
- **A-6** Added `expires_at` field to `TokenConfig` and `Principal`. Auth middleware rejects expired tokens with `401 token expired`. Added `POST /v1/auth/token` endpoint (admin:config scope) for minting short-lived scoped tokens.

### Phase B — Local Device Integrations
- **B-1** Added Shelly collector (`internal/collector/shelly.go`) polling Gen2+ devices over local HTTP RPC for temperature and humidity. Config: `shelly.devices[]` with `id`, `ip`, `label`, `enabled`. Feature flag: `POLAR_ENABLE_SHELLY=true`.
- **B-2** Added SwitchBot OpenAPI collector (`internal/collector/switchbot.go`) supporting Meter, MeterPlus, CO2 Sensor, and Hub 2 devices. Auth: HMAC-SHA256 signed requests per OpenAPI v1.1 spec. Feature flag: `POLAR_ENABLE_SWITCHBOT=true`.
- **B-3** Added `MultiCollector` (`internal/collector/multi.go`) that merges readings from all active collectors. Main wires Airthings, Shelly, SwitchBot, and Netatmo through it, falling back to simulator when none are configured.
- **C-5** Added Netatmo Weather Station collector (`internal/collector/netatmo.go`) supporting indoor base stations (temperature, humidity, CO2, noise, pressure), outdoor modules (temperature, humidity), wind gauges (wind speed, gusts), and rain gauges (rain rate, 1h accumulation). Auth: OAuth 2.0 refresh-token flow with automatic token rotation. Config: `netatmo.client_id`, `netatmo.client_secret`, `netatmo.refresh_token`, `netatmo.device_ids`. Feature flag: `POLAR_ENABLE_NETATMO=true`.

### Phase C — Explanatory Public Context
- **C-4** Added `AstronomyProvider` (`internal/providers/astronomy.go`) computing sunrise, sunset, civil twilight, and solar noon using the NOAA solar-position algorithm — fully in-process with no API key. Results are embedded in `OutdoorClimate.Astronomy` and served at `GET /v1/astronomy/today` and `get_astronomy` MCP method.
- **C-1** Added `FIRMSProvider` (`internal/providers/firms.go`) fetching NASA FIRMS VIIRS hotspot CSV. Computes nearest hotspot distance, active hotspot count within radius, and a risk level. Embedded in `OutdoorClimate.Wildfire`. Endpoint: `GET /v1/wildfire/current`.
- **C-2** Added pollen index support via `WeatherAPIClient` (`internal/providers/pollen.go`). Tree, grass, and weed indices embedded in `OutdoorClimate.Pollen`. Endpoint: `GET /v1/pollen/current`.
- **C-3** Added UV index from WeatherAPI, embedded in `OutdoorClimate.UV`. Endpoint: `GET /v1/uv/current`.
- **C-6** Added `PurpleAirProvider` (`internal/providers/purpleair.go`) querying sensors within a configurable radius and averaging PM2.5/PM10. Embedded in `OutdoorClimate.PurpleAir`. Endpoint: `GET /v1/air-quality/neighborhood`.
- Added `WildfireContext`, `PollenContext`, `UVContext`, `PurpleAirAQ`, and `AstronomyContext` to contracts. Added `ForecastPoint.WindDirectionDeg` and `ForecastPoint.PrecipitationProbabilityPct`.
- `ClimateSnapshotForTarget` now includes all enabled Phase C context fields automatically.

### Cross-Cutting Quality and Security
- **X-5** Added cross-target authorization enforcement. `TokenConfig` now accepts `allowed_targets []string`; tokens with a non-empty list may only access the named targets. `Principal.CanAccessTarget` / `Auth.AuthorizeTarget` enforce this at the middleware level. All 12 target-parameterized REST handlers and all 12 target-using MCP methods resolve the target (falling back to the configured default) and gate access before dispatching. Backward-compatible: tokens without `allowed_targets` continue to access all targets.
- **X-2** Added comprehensive authorization negative test suite (`test/auth_test.go`). 12 test functions, 120+ subtests covering: no token → 401 on every protected REST route; wrong scope → 403 across all scope boundaries (telemetry/forecast/audit/admin); expired token → 401; cross-target isolation (X-5) for all target-parameterized routes on both REST and MCP; WebSocket upgrade rejection without auth or with wrong scope; WebSocket target isolation; MCP no-token → HTTP 401; MCP wrong scope → JSON-RPC -32003 for all method/scope combinations; unknown token → 401; wildcard scope passes all scope checks.
- **X-1** Added provider contract fixture test suite. `test/fixtures/` contains recorded JSON responses for NOAA (points, stations, current observations, hourly forecast, alerts), Open-Meteo, AirNow (current + daily forecast), Shelly, and SwitchBot. `test/contract_test.go` spins up per-test `httptest.Server` instances and validates that each adapter produces correctly populated `ForecastSnapshot`, `WeatherCurrent`, `AirQualityCurrent`, and `Reading` contracts. NOAA fixture embeds a `{SERVER_URL}` placeholder resolved at runtime. `SwitchBotService` gained a `SetBaseURL` method for test redirection.
- **X-3** Added freshness SLO monitoring. `FreshnessSLOConfig` (`indoor_max_age_s`/`weather_max_age_s`/`aq_max_age_s`, defaults 300/1200/3600) configurable via env vars. `GET /v1/diagnostics/data-gaps` now returns `DiagnosticsReport` (includes both `data_gaps` and `slo_breaches`). `Service.checkSLOBreaches` checks indoor/weather/AQ data ages against thresholds; each breach increments `obs.Metrics.sloBreaches` and causes `StationHealth` to report `degraded`. `SLOSnapshot` added to metrics snapshot.
- **X-4** Added consent and retention policy table. `ConsentGrant` contract type records active integration grants with scopes, data classes, retention policy, and sharing flags. `consent_grants` table added to both SQLite and Postgres migrations (unique on `target_id+provider`). `Repository.UpsertConsentGrant` / `ListConsentGrants`. `Service.SeedConsentGrants` builds grants from current provider config and upserts at startup. `GET /v1/consent/grants` (admin:config) and `list_consent_grants` MCP method expose the full grant list.

### Command idempotency and state machine
- **Idempotency enforcement** — `SubmitCommand` now checks `FindCommandByIdempotencyKey` before inserting; if a non-empty `idempotency_key` matches an existing command for the same target, the existing command is returned without creating a duplicate.
- **`UpdateCommandStatus`** — new service method that validates the command is not already in a terminal state (`succeeded`, `failed`, `rejected`, `expired`), writes the new status to `commands`, and upserts a `CommandResult` row — preserving `accepted_at` across subsequent transitions.
- **`PATCH /v1/commands/{id}/status`** — REST endpoint to advance a command's lifecycle; body: `{"status":"accepted","observed_effect":"...","error":"..."}`. Routes within the existing `/v1/commands/` prefix handler.
- **MCP `update_command_status`** — accepts `command_id`, `status`, `observed_effect`, `error`; returns updated command + result envelope.
- **Repository additions** — `FindCommandByIdempotencyKey` (lookup by target + key), `UpdateCommandStatus` (UPDATE with rows-affected check).
- **`test/idempotency_test.go`** — 7 test functions: same-key returns same ID, different keys produce different IDs, no key always creates new, full `pending→accepted→executing→succeeded` transition, terminal-state block (failed → pending → 400), rejected state with error, `accepted_at` preservation across multi-step lifecycle, PATCH scope enforcement (no-token → 401), MCP `update_command_status` happy path.
- **`AGENTS.md`** — Fully rewritten: auth and token rotation guide, complete REST API table (all routes, scopes, descriptions), complete MCP method table, freshness SLO env var reference, validation instructions.

### Test suites (D-1 + freshness/fallback)
- **commands_test.go** Added 9 command-plane test functions covering: submit happy path, validation rejections (missing capability, unknown target, negative TTL), get by ID (with and without a result), list, `write:commands` scope enforcement table (6 sub-cases: no-token → 401, telemetry → 403, cmds → allowed), target isolation (home-only token blocked from cabin), MCP `submit_command`/`get_command`/`list_commands`, TTL round-trip, and `UpsertCommandResult`/`GetCommandResult` repo round-trip via REST get.
- **freshness_test.go** Added 9 freshness, SLO, and fallback test functions: `DiagnosticsReport` shape (keys present, `generated_at` non-zero), weather SLO breach via seeded stale provider status, AQ SLO breach, no-breach when fresh, `StationHealth` degraded on SLO breach (with "slo:" component entry), healthy when all providers are fresh, forecast stale flag on provider failure, climate snapshot graceful degradation when all providers fail (returns 200 with indoor data intact), SLO metrics counter accumulation across multiple `DataGaps` calls.
- **auth_test.go** Extended no-token (401) suite with `/v1/consent/grants`, `POST /v1/commands`, `GET /v1/commands`, `GET /v1/commands/{id}`. Extended wrong-scope (403) suite with `telemetry→commands` case.

### Phase D — Control Plane
- **D-1** Added command plane schema and plumbing. New `Command`, `CommandResult`, `CommandActor`, and `CommandStatus` contract types define the write-side data model. `commands` and `command_results` tables added to both SQLite and Postgres migrations. Repository: `InsertCommand`, `GetCommand`, `ListCommandsForTarget`, `UpsertCommandResult`, `GetCommandResult`. Service: `SubmitCommand` (validates target + expiry, stores status "pending"), `GetCommand`, `ListCommands`. New `write:commands` scope. REST: `POST /v1/commands` (submit), `GET /v1/commands` (list by target), `GET /v1/commands/{id}` (fetch with result). MCP: `submit_command`, `get_command`, `list_commands`. No device integrations wired — schema + plumbing only.

### Added `ISSUES.md` with a full set of phased work items derived from RESEARCH.md, covering platform tightening, local device integrations, explanatory public context, and a future control plane.
### Updated `ROADMAP.md` to reflect the complete four-phase plan (A: platform tightening, B: local devices, C: public context, D: control plane) with cross-cutting quality and security items.

## Unreleased (prior)

- Increased `health_check` timeout from 30s to 60s to give the Go service enough time to initialize on first start after a registry pull.
- Ignored the repository-root `blink.toml` and `BLINK.md` and stopped tracking them so homelab-specific Blink targets and operator notes stay local-only.
- Collapsed REST and MCP onto a single port (`6703`). `main.go` now mounts the MCP handler at `/mcp` on the REST mux when `POLAR_MCP_ADDR == POLAR_LISTEN_ADDR` instead of starting a second server. `blink.toml` updated: port `6702` → `6703`, second `-p` publish and separate `POLAR_MCP_ADDR` removed.

## 0.2.0 - 2026-03-29

- Added target-aware monitoring for dashboard-focused home and area climate views
- Added NOAA-backed weather current, forecast, and alerts integration with Open-Meteo fallback forecasting
- Added AirNow-backed external air-quality current and forecast integration
- Added WebSocket live feed support for target climate snapshots
- Added provider status tracking and storage for outdoor snapshots and alerts
- Added Postgres-ready storage support alongside existing SQLite support
- Expanded REST, MCP, docs, config examples, and deployment env contracts for the new monitoring model

## 0.1.0 - 2026-03-06

- Initial Polar implementation scaffold
- Added simulator ingestion for environmental metrics
- Added SQLite persistence layer and migrations
- Added Open-Meteo forecast integration
- Added REST and MCP server surfaces
- Added config system, auth middleware, and health endpoints
- Added CI workflow, Makefile, and systemd deployment template
