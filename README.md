# Polar

Polar is an AI-first weather, climate, and environmental monitoring toolkit built in Go.

## Current Implementation (v1 foundation)

- Simulator-based environmental sensor ingestion (temperature, humidity, light, wind speed, pressure, rainfall)
- SQLite-backed local persistence with migrations
- Open-Meteo forecast polling
- REST API with token auth
- MCP-style JSON-RPC server with tool methods
- Health endpoints and audit event storage

## Quickstart

```bash
go mod tidy
make run
```

Auth token default: `dev-token`

REST health: `GET http://localhost:8080/healthz`
MCP health: `GET http://localhost:8081/healthz`

## REST Endpoints

- `GET /v1/capabilities`
- `GET /v1/station/health`
- `GET /v1/readings/latest`
- `GET /v1/readings?metric=&from=&to=`
- `GET /v1/forecast/latest`
- `GET /v1/forecast?from=&to=`
- `GET /v1/diagnostics/data-gaps`
- `GET /v1/audit/events?from=&to=&type=`

All `/v1/*` endpoints require:

`Authorization: Bearer <token>`

## MCP Tools

- `list_capabilities`
- `get_station_health`
- `get_latest_readings`
- `query_readings`
- `get_forecast`
- `get_data_gaps`
- `get_audit_events`

POST JSON-RPC requests to `/mcp` on port 8081.
