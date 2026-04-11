# Changelog

## Unreleased

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
