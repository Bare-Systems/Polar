# Polar

Polar is an AI-first weather, climate, and environmental monitoring toolkit built in Go.

## Current Implementation (v1 foundation)

- Simulator-based environmental sensor ingestion (temperature, humidity, light, wind speed, pressure, rainfall)
- SQLite-backed local persistence with migrations
- Open-Meteo forecast polling
- REST API with scoped token auth
- MCP-style JSON-RPC server with tool methods
- Health endpoints and audit event storage

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

## REST Endpoints

- `GET /v1/capabilities`
- `GET /v1/station/health`
- `GET /v1/readings/latest`
- `GET /v1/readings?metric=&from=&to=&resolution=`
- `GET /v1/forecast/latest`
- `GET /v1/forecast?from=&to=`
- `GET /v1/diagnostics/data-gaps`
- `GET /v1/audit/events?from=&to=&type=`
- `GET /v1/metrics`

All `/v1/*` endpoints require:

`Authorization: Bearer <token>`

Scope mapping:

- `read:telemetry` for capabilities, station health, readings, and data gaps
- `read:forecast` for forecast endpoints
- `read:audit` for audit events
- `admin:config` for metrics

## MCP Tools

- `list_capabilities`
- `get_station_health`
- `get_latest_readings`
- `query_readings`
- `get_forecast`
- `get_data_gaps`
- `get_audit_events`
- `get_metrics`

POST JSON-RPC requests to `/mcp` on port 8081.
