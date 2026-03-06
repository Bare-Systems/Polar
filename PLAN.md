# Polar - Production Master Plan

## 1. Vision

Build **Polar** as an AI-first, embedded-ready weather/climate/environment toolkit that can run reliably on Linux edge devices, ingest real sensor data, enrich with public weather APIs, and expose stable machine interfaces (REST + MCP) for AI systems and service integrations (Koala, Panda, and future systems).

Polar must be deployable in unattended environments and engineered for production stability, security, and observability.

## 2. Product Goals and Constraints

### Core goals
- [x] Continuous local environmental sensing (temperature, humidity, light, wind speed, pressure, rainfall; extensible metric set).
- [ ] Reliable storage of raw and derived telemetry with offline-first behavior.
- [x] Weather forecast enrichment from pluggable providers (Open-Meteo first).
- [x] Unified contracts for AI and systems integrations via REST and MCP.
- [ ] Secure operation over LAN/WAN with mTLS and scoped auth.
- [ ] Operationally diagnosable under failure conditions.

### Non-goals for v1
- [ ] Autonomous control loops that can mutate hardware state without human approval.
- [ ] Browser dashboard UI as a hard requirement.
- [ ] Cloud-only dependency for normal operation.

### Constraints
- [x] Primary implementation language: **Go**.
- [x] Primary deployment target: Raspberry Pi class Linux SBC.
- [x] Single-node runtime first; clustered HA can be phase-2+.
- [ ] Local durability: SQLite primary + export mechanisms.

---

## 3. Current State (as implemented)

### Already implemented
- [x] Go repo bootstrap with modular packages.
- [x] Single binary service startup.
- [x] Simulator sensor ingestion for six metrics.
- [x] SQLite migrations and persistence for readings/forecast/audit events.
- [x] Open-Meteo forecast polling.
- [x] REST endpoints for core reads/health/audit.
- [x] MCP JSON-RPC server with tool methods.
- [x] Bearer token middleware.
- [x] Basic CI workflow and systemd template.

### Not yet production complete
- [ ] Real hardware protocol adapters.
- [ ] mTLS and robust authz/scoping.
- [ ] Offline queue/circuit breaker/recovery workflows.
- [ ] Prometheus-grade observability.
- [ ] Strict schema/version lifecycle and compatibility testing.
- [ ] Packaging hardening (SBOM, signatures, reproducible builds).
- [ ] Full operational runbooks and acceptance gates.

---

## 4. Target Production Architecture

### Runtime topology
- [ ] `polar` single process composed of subsystems:
  - collector
  - ingestion pipeline
  - provider client manager
  - storage/repository
  - REST API server
  - MCP server
  - authN/authZ
  - scheduler
  - observability
  - export/sync workers

### Data flow
1. Sensor adapters emit normalized readings.
2. Ingestion validates, annotates quality, persists append-only records.
3. Provider manager periodically fetches forecasts and stores snapshots.
4. Query services expose latest state/history/forecast via REST and MCP.
5. Observability tracks all pipeline stages.
6. Outbound sync/export executes asynchronously with retry/circuit policies.

### Design principles
- [x] Shared domain service layer for REST and MCP (single behavior source).
- [ ] Explicit unit handling and schema versioning.
- [ ] Fail-closed on security, fail-open only for non-critical enrichment.
- [ ] No silent failures; all failures observable in logs/metrics/audit.

---

## 5. Contract and Interface Standards

### REST baseline
- [x] `GET /healthz`
- [ ] `GET /readyz`
- [x] `GET /v1/capabilities`
- [x] `GET /v1/station/health`
- [x] `GET /v1/readings/latest`
- [ ] `GET /v1/readings?metric=&from=&to=&resolution=`
- [x] `GET /v1/forecast/latest`
- [x] `GET /v1/forecast?from=&to=`
- [x] `GET /v1/diagnostics/data-gaps`
- [x] `GET /v1/audit/events?from=&to=&type=`

### MCP baseline tools
- [x] `list_capabilities`
- [x] `get_station_health`
- [x] `get_latest_readings`
- [x] `query_readings`
- [x] `get_forecast`
- [x] `get_data_gaps`
- [x] `get_audit_events`

### Contract policies
- [ ] SemVer for API contract set and MCP tool schema set.
- [ ] Backward compatibility guaranteed for one minor line.
- [ ] Deprecation window: minimum 2 minor releases.
- [ ] JSON schema docs generated and versioned in-repo.

---

## 6. Security Model (Production)

### Authentication
- [ ] mTLS required for service-to-service traffic in production mode.
- [ ] Scoped bearer/service tokens for request authorization.
- [ ] Token scopes mapped to operation categories (`read:telemetry`, `read:forecast`, `read:audit`, `admin:config`).

### Authorization
- [ ] Default deny policy.
- [ ] Route/tool-level scope checks.
- [ ] Audit all auth failures with source identity and reason.

### Secrets management
- [ ] No secrets in repo.
- [ ] Secrets loaded from env/file mounts.
- [ ] Secret redaction in logs and panic traces.

### Transport security
- [ ] TLS 1.3 minimum in prod profile.
- [ ] Strong cipher policy.
- [ ] Certificate rotation runbook and automated validation.

### Hardening controls
- [ ] Input validation on all external interfaces.
- [ ] Request size/time limits and replay resistance for sensitive endpoints.
- [ ] Structured security event log stream.

---

## 7. Reliability and Stability Targets

### SLO targets (v1 production)
- [ ] Local ingestion availability: **99.9%** monthly.
- [ ] API availability (local edge network): **99.5%** monthly.
- [ ] Data durability under 24h network outage: **no silent loss** within configured buffer limits.
- [ ] Startup to ready state: under **20s** on target SBC.

### Error budgets
- [ ] Define monthly error budget per SLO.
- [ ] Use budget burn alerts to gate feature rollout.

### Offline-first requirements
- [ ] Continue sampling with no internet.
- [ ] Forecast marked stale when provider unavailable.
- [ ] Queue outbound jobs for retry with bounded backoff.

### Recovery expectations
- [ ] Graceful restart without telemetry corruption.
- [ ] SQLite integrity checks on startup.
- [ ] Deterministic degraded mode states.

---

## 8. Data and Storage Strategy

### Storage layers
- [x] SQLite primary operational store.
- [x] Append-only raw telemetry records.
- [x] Forecast snapshots persisted with provider metadata.
- [x] Audit events immutable.

### Data lifecycle
- [ ] Retention policies for raw and aggregated data.
- [ ] Rollup jobs for long-term historical summaries.
- [ ] Optional periodic Parquet export for analytics portability.

### Integrity guarantees
- [ ] Transactional writes for batched ingestion.
- [ ] Idempotency keys to prevent duplicate insertion during retries.
- [ ] Startup and periodic integrity validation.

---

## 9. Observability Standards

### Logging
- [ ] Structured JSON logs.
- [ ] Correlation/request IDs across REST/MCP/worker pipelines.
- [ ] Severity levels with stable machine parsable fields.

### Metrics (Prometheus)
- [ ] Ingestion throughput and latency.
- [ ] Sensor read failures by adapter/metric.
- [ ] Forecast fetch latency/failures/cache freshness.
- [ ] Queue depth and retry counters.
- [ ] DB read/write latency and error rates.
- [ ] Auth failures and rate-limit rejects.

### Health endpoints
- [x] `healthz`: process alive.
- [ ] `readyz`: dependencies and subsystem readiness.
- [x] subsystem health breakdown in station health payload.

### Tracing (phase 2)
- [ ] OpenTelemetry spans for API and critical workers.

---

## 10. Configuration and Profiles

### Profile types
- [x] `simulator`
- [x] `pi-basic`
- [x] `pi-extended`
- [ ] `prod-edge` (to be added)

### Config precedence
1. Runtime flags
2. Environment variables
3. Config file
4. Defaults

### Validation rules
- [x] Required station identity and coordinates.
- [x] Sampling intervals bounded by safety constraints.
- [ ] Provider and auth settings must be internally consistent.
- [x] Invalid configuration fails fast at startup.

---

## 11. Full Production Roadmap

## Phase 0 - Project Baseline Completion
- [x] Normalize repository layout and coding conventions.
- [ ] Add contributor docs and branching/release conventions.
- [x] Finalize Make targets and CI parity with local workflows.
- [ ] Exit criteria:
  - Clean `go test ./...`, `go vet ./...`, and lint pipeline in CI.
  - Reproducible local run command documented.

## Phase 1 - Domain Contracts Hardening
- [ ] Finalize canonical entities and unit catalog.
- [ ] Add schema version headers/fields in API and MCP results.
- [ ] Implement contract compatibility tests.
- [ ] Generate versioned JSON schema artifacts.
- [ ] Exit criteria:
  - Contract freeze `v1alpha1` complete.
  - Compatibility test suite blocks breaking changes.

## Phase 2 - Config System Hardening
- [ ] Introduce strict typed config parser with profile inheritance.
- [ ] Add comprehensive config lint/validation command.
- [ ] Add startup config fingerprinting (excluding secrets) for auditability.
- [ ] Exit criteria:
  - All invalid-config classes covered by tests.

## Phase 3 - Storage Robustness
- [ ] Add migrations framework with version table and rollback guidance.
- [ ] Add idempotent insert semantics.
- [ ] Add retention and rollup workers.
- [ ] Add Parquet export worker and failure telemetry.
- [ ] Exit criteria:
  - Restart/durability test suite passes.
  - Retention behavior validated in integration tests.

## Phase 4 - Ingestion Pipeline Productionization
- [ ] Introduce ingestion queue abstraction and bounded backpressure.
- [ ] Add per-metric quality rules and outlier handling.
- [ ] Add dedupe safeguards and ingestion dead-letter flow.
- [ ] Exit criteria:
  - Sustained load tests pass at target sampling rates.

## Phase 5 - Hardware Adapter Framework
- [ ] Define adapter lifecycle and capability contract.
- [ ] Implement protocol abstraction layers for I2C/SPI/UART/USB.
- [ ] Provide first-party adapters for selected baseline sensors.
- [ ] Add adapter simulation shims for CI.
- [ ] Exit criteria:
  - At least one real sensor path validated for each protocol family.

## Phase 6 - Forecast Provider Manager
- [ ] Provider interface with failover policy support.
- [ ] Open-Meteo hardening with retries, cache TTL, and stale markers.
- [ ] Optional NOAA/NWS adapter (US profile).
- [ ] Add provider health and circuit state metrics.
- [ ] Exit criteria:
  - Provider outage scenarios pass chaos tests.

## Phase 7 - REST API Hardening
- [ ] Add strict request validation and typed errors.
- [ ] Add pagination and query limits for large history reads.
- [ ] Add response compression and timeout policies.
- [ ] Publish and validate OpenAPI contract in CI.
- [ ] Exit criteria:
  - REST compatibility tests and negative tests pass.

## Phase 8 - MCP Server Hardening
- [x] Align MCP tool schemas with REST domain contracts.
- [ ] Add tool-level authz and per-tool rate limiting.
- [ ] Add MCP compatibility tests with reference client harness.
- [ ] Exit criteria:
  - MCP contract tests pass across supported model runtimes.

## Phase 9 - AuthN/AuthZ and TLS Production Security
- [ ] Implement mTLS profile and cert verification chain.
- [ ] Implement scoped token model with expiration and rotation.
- [ ] Add security middleware for replay and signature options.
- [ ] Add security audit event taxonomy.
- [ ] Exit criteria:
  - Security integration suite passes.
  - Threat model v1 reviewed and signed off.

## Phase 10 - Reliability and Recovery
- [ ] Add write-ahead queue for outbound workflows.
- [ ] Add circuit breaker primitives with half-open logic.
- [ ] Implement startup recovery and corruption handling strategy.
- [ ] Add deterministic degraded mode transitions.
- [ ] Exit criteria:
  - Fault injection tests validate recovery playbooks.

## Phase 11 - Observability and Ops Readiness
- [ ] Finalize metrics package with alertable signals.
- [ ] Add standardized dashboards/alert rules definitions.
- [ ] Add runbooks for top incident classes.
- [ ] Add log-volume and cardinality controls.
- [ ] Exit criteria:
  - On-call dry-run can diagnose seeded failures.

## Phase 12 - Koala/Panda Integration Program
- [ ] Define compatibility profile docs for each consumer.
- [ ] Add adapter layer for naming/shape mismatches.
- [ ] Build end-to-end integration test fixtures.
- [ ] Define compatibility matrix and deprecation policy.
- [ ] Exit criteria:
  - Koala and Panda consume Polar contracts without custom patching.

## Phase 13 - Packaging and Supply Chain
- [ ] Produce reproducible binaries for linux/amd64 and linux/arm64.
- [ ] Add container image build path.
- [ ] Add SBOM generation and artifact checksums.
- [ ] Add artifact signing and provenance metadata.
- [ ] Exit criteria:
  - Release pipeline outputs signed artifacts and SBOMs.

## Phase 14 - Edge Deployment and Fleet Ops
- [ ] Finalize systemd service and boot-time behaviors.
- [ ] Add install/upgrade/rollback scripts.
- [ ] Add backup/restore procedure for local DB.
- [ ] Add config rollout strategy for multi-device fleets.
- [ ] Exit criteria:
  - Documented day-2 operations validated on staging devices.

## Phase 15 - Performance and Endurance Qualification
- [ ] Long-run endurance test (7-30 days).
- [ ] CPU/memory/storage profile under realistic mixed load.
- [ ] Thermal and throttling behavior validation on SBC hardware.
- [ ] Exit criteria:
  - Resource targets met with margin on target hardware.

## Phase 16 - Security Hardening and Audit
- [ ] Static and dynamic security scans in CI/CD.
- [ ] Dependency vulnerability scanning and policy gates.
- [ ] Pen-test checklist execution.
- [ ] Incident response playbook validation.
- [ ] Exit criteria:
  - No unresolved high/critical findings for release.

## Phase 17 - Release Candidate and Stabilization
- [ ] Freeze API/MCP schemas for `v1.0.0`.
- [ ] Strict bug triage and release blocker process.
- [ ] Full regression and chaos suite execution.
- [ ] Release notes with known limitations and migration notes.
- [ ] Exit criteria:
  - Release candidate meets all go/no-go gates.

## Phase 18 - v1 GA and Post-GA Operations
- [ ] Tag and publish `v1.0.0`.
- [ ] Post-launch monitoring and rapid patch policy.
- [ ] Define v1.1 priorities from operational telemetry.
- [ ] Exit criteria:
  - Stable production operation over first 30-day window.

---

## 12. Testing and Verification Program

### Unit tests
- [ ] Domain validation and unit handling.
- [ ] Config parsing, precedence, and validation.
- [ ] Repository operations and migration correctness.
- [ ] Auth scope and middleware behavior.

### Integration tests
- [ ] End-to-end simulator ingest -> storage -> REST/MCP query.
- [ ] Provider integration with success, timeout, malformed payload, and outage scenarios.
- [ ] Auth integration with valid/invalid certs/tokens.

### Hardware-in-loop tests
- [ ] Real sensor stability and calibration persistence.
- [ ] Protocol disruption handling for I2C/SPI/UART/USB.
- [ ] Cold boot and warm restart behavior on SBC.

### Reliability tests
- [ ] 24h+ offline mode with uninterrupted local ingestion.
- [ ] DB corruption and recovery drills.
- [ ] Queue backpressure and retry exhaustion scenarios.

### Performance tests
- [ ] Throughput at target sampling frequencies.
- [ ] Query latency under concurrent load.
- [ ] Resource footprint under long-running operation.

### Security tests
- [ ] Auth bypass attempts.
- [ ] Input fuzzing for REST and MCP payloads.
- [ ] TLS/cert misconfiguration handling.
- [ ] Secret leakage checks.

### Acceptance gates
- [ ] No release-blocking defects open.
- [ ] SLO validation evidence captured.
- [ ] Security and reliability gates passed.
- [ ] Contract compatibility suites green.

---

## 13. Operational Readiness Requirements

### Required runbooks
- [ ] installation
- [ ] configuration management
- [ ] cert and token rotation
- [ ] backup and restore
- [ ] incident triage
- [ ] data integrity check and repair
- [ ] controlled shutdown and restart
- [ ] rollback to prior release

### Incident classes
- [ ] sensor adapter failure
- [ ] provider outage
- [ ] auth misconfiguration
- [ ] storage corruption
- [ ] high resource usage
- [ ] clock drift/time sync issues

### Alerting baseline
- [ ] ingestion stalled
- [ ] queue depth over threshold
- [ ] provider stale beyond SLA window
- [ ] repeated auth failures
- [ ] storage error spikes

---

## 14. Release Management and Branch Strategy

### Branching
- [ ] `main`: protected, releasable.
- [ ] `codex/*` or `feature/*`: short-lived development branches.
- [ ] `release/*`: stabilization branches when required.

### Versioning
- [ ] SemVer for binaries and contracts.
- [ ] Changelog required on release PRs.

### Release artifacts
- [x] binaries (linux/amd64, linux/arm64)
- [ ] checksums
- [ ] SBOM
- [ ] signed provenance/attestation
- [x] deployment manifests/templates

---

## 15. Risk Register and Mitigations

### Key risks
- [ ] Hardware adapter variability and driver instability.
- [ ] Sensor drift and calibration uncertainty.
- [ ] Long-lived SQLite contention under mixed load.
- [ ] API/provider schema drift.
- [ ] Security misconfiguration in edge deployments.

### Mitigations
- [ ] Adapter conformance tests and compatibility matrix.
- [ ] Calibration metadata and scheduled calibration reminders.
- [ ] DB tuning, retention, and rollup strategy.
- [ ] Provider abstraction with strict response validation.
- [ ] Secure-by-default profiles and startup policy checks.

---

## 16. Go/No-Go v1 Production Checklist

- [ ] All mandatory phases (0-17) complete or explicitly waived with risk acceptance.
- [ ] REST and MCP contract suites green against frozen v1 schemas.
- [ ] mTLS + scoped auth operational in production profile.
- [ ] 24h offline reliability validation passed.
- [ ] Endurance test completed with resource limits within thresholds.
- [ ] High/critical security findings remediated.
- [ ] Runbooks complete and operator dry-run executed.
- [ ] Signed release artifacts and SBOM published.

---

## 17. Execution Cadence

### Recommended delivery rhythm
- [ ] 2-week engineering sprints.
- [ ] Weekly architecture/risk review.
- [ ] Milestone-end readiness review with evidence package.

### Evidence required per milestone
- [ ] test results
- [ ] performance/reliability metrics snapshots
- [ ] security review deltas
- [ ] updated documentation and runbooks

---

## 18. Immediate Next Actions (from current state)

1. Finish Phase 1 contract hardening and schema versioning.
2. Implement mTLS + scoped authorization model (Phase 9 priority pull-in).
3. Build adapter framework and first real sensor integrations (Phase 5).
4. Add reliability primitives (queue/circuit/recovery) and chaos tests (Phase 10).
5. Complete observability package and alert/runbook baseline (Phase 11).
