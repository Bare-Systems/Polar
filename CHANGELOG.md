# Changelog

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
