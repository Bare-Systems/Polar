# Security Policy

Polar exposes authenticated REST and MCP surfaces and may run unattended on edge infrastructure.

## Reporting

Report vulnerabilities privately with:

- affected endpoint or subsystem
- token or auth scope behavior
- reproduction steps
- expected versus actual protection

## Baseline Expectations

- Protected routes require documented auth behavior.
- Operational failures should be diagnosable without leaking secrets.
- Deployment and network posture changes must update `BLINK.md` and `README.md`.
