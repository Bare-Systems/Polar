# Polar

Polar is the BareSystems dashboard-focused environmental telemetry service. It monitors a small set of configured target areas, stores current and historical climate data, and serves that data back to dashboards and agents without forcing every client to hit NOAA or AirNow directly.

## Current Implementation

- Indoor climate ingestion from the simulator or Airthings
- Target-aware outdoor weather polling with NOAA as primary source
- Open-Meteo forecast fallback when NOAA forecast pulls fail
- Target-aware external air quality polling from AirNow
- Historical persistence for readings, forecasts, AQ snapshots, alerts, and provider status
- REST API, MCP JSON-RPC API, and a WebSocket live feed
- SQLite for local/dev and Postgres-ready storage support for deployed environments

## Quickstart

```bash
go mod tidy
make run
```

Auth token default: `dev-token`

Scoped tokens are also supported through `auth.tokens` in config JSON:

```json
{
  "auth": {
    "tokens": [
      {"name": "telemetry-reader", "value": "telemetry-token", "scopes": ["read:telemetry"]},
      {"name": "forecast-reader", "value": "forecast-token", "scopes": ["read:forecast"]},
      {"name": "ops-admin", "value": "admin-token", "scopes": ["admin:config"]}
    ]
  }
}
```

REST health: `GET http://localhost:8080/healthz`
REST readiness: `GET http://localhost:8080/readyz`
MCP health: `GET http://localhost:8081/healthz`
Live feed: `GET ws://localhost:8080/v1/live?target=<target-id>`

## Configuration Notes

- `storage.driver=sqlite` with `storage.sqlite_path` is the local default.
- Production deployments can use `storage.driver=postgres` with `storage.database_url`.
- NOAA requests require `provider.noaa_user_agent`.
- AirNow data requires `provider.airnow_token`.
- Monitored dashboard areas are configured in `monitoring.targets`.
- If `monitoring.targets` is omitted, Polar seeds a single default target from `station`.

## REST Endpoints

- `GET /v1/capabilities`
- `GET /v1/targets`
- `GET /v1/station/health`
- `GET /v1/readings/latest`
- `GET /v1/readings?metric=&from=&to=&resolution=`
- `GET /v1/forecast/latest?target=`
- `GET /v1/weather/current?target=`
- `GET /v1/weather/forecast?target=`
- `GET /v1/weather/alerts?target=`
- `GET /v1/air-quality/current?target=`
- `GET /v1/air-quality/forecast?target=`
- `GET /v1/climate/snapshot?target=`
- `GET /v1/live?target=` (WebSocket)
- `GET /v1/diagnostics/data-gaps`
- `GET /v1/audit/events?from=&to=&type=`
- `GET /v1/metrics`

All `/v1/*` endpoints require:

`Authorization: Bearer <token>`

Scope mapping:

- `read:telemetry` for capabilities, targets, station health, live feed, readings, current weather, current air quality, alerts, climate snapshot, and data gaps
- `read:forecast` for weather forecast and air-quality forecast
- `read:audit` for audit events
- `admin:config` for metrics

## MCP Tools

- `list_capabilities`
- `list_targets`
- `get_station_health`
- `get_latest_readings`
- `query_readings`
- `get_forecast`
- `get_weather_current`
- `get_weather_forecast`
- `get_weather_alerts`
- `get_air_quality_current`
- `get_air_quality_forecast`
- `get_climate_snapshot`
- `get_data_gaps`
- `get_audit_events`
- `get_metrics`

POST JSON-RPC requests to `/mcp` on port 8081.
