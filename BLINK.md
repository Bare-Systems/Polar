# Polar Blink Contract

This file documents the real behavior of [`blink.toml`](/Users/joecaruso/Projects/BareSystems/Polar/blink.toml).

## Target

- `homelab`
- type: SSH
- host: `beelink`
- user: `admin`
- runtime dir: `/home/admin/baresystems/runtime/polar`

## Build Sources

`polar` supports:

- `linux-amd64` (default): cross-build locally, package into a container image, and push to the registry
- `linux-arm64`: cross-build a native ARM64 binary artifact

Build behavior notes:

- The `linux-amd64` build now cross-compiles natively from the host architecture instead of compiling inside an emulated `linux/amd64` Go container.
- Go module and build caches persist under `.gocache/` in the workspace to keep repeat builds fast.
- The runtime image now uses `alpine` with `ca-certificates` only, avoiding the slower Debian `apt-get` path on every build.

## Deploy Behavior

Pipeline:

- `provision`
- `stop`
- `start`
- `health_check`
- `verify`

Rollback pipeline:

- `stop`
- `start`
- `health_check`

The deploy flow provisions runtime directories and a seeded env file, replaces the running container, then verifies REST and MCP health.

## Runtime Expectations

- Polar now supports both SQLite and Postgres-backed storage.
- The deployed container still seeds a local data mount for compatibility, but production operators should set `POLAR_STORAGE_DRIVER=postgres` and `POLAR_DATABASE_URL` in `polar.env` when Postgres is available on the stack.
- NOAA access requires `POLAR_NOAA_USER_AGENT`.
- AirNow access requires `POLAR_AIRNOW_TOKEN`.
- Live dashboard updates are served from the same REST port at `/v1/live`.

## Verification

- REST health on `6702`
- MCP health on `6703`
- container running state
- auth-required behavior on protected reads
- target list endpoint responds on the published REST port

## Operator Notes

- Polar is currently deployed as a container on the homelab host.
- The env file is seeded once and should be filled with real credentials on the host.
- Update this file whenever ports, build sources, runtime image behavior, or verification checks change.
