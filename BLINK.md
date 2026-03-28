# Polar Blink Contract

This file documents the real behavior of [`blink.toml`](/Users/joecaruso/Projects/BareSystems/Polar/blink.toml).

## Target

- `homelab`
- type: SSH
- host: `blink`
- user: `admin`
- runtime dir: `/home/admin/baresystems/runtime/polar`

## Build Sources

`polar` supports:

- `linux-amd64` (default): cross-build locally, package into a container image, and push to the registry
- `linux-arm64`: cross-build a native ARM64 binary artifact

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

## Verification

- REST health on `6702`
- MCP health on `6703`
- container running state
- auth-required behavior on protected reads

## Operator Notes

- Polar is currently deployed as a container on the homelab host.
- The env file is seeded once and should be filled with real credentials on the host.
- Update this file whenever ports, build sources, runtime image behavior, or verification checks change.
