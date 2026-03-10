# Contributing to Polar

## Development Setup

```bash
go mod tidy
make run        # simulator profile, REST :8080, MCP :8081
make test       # unit + integration tests
make race       # tests with race detector
make lint       # golangci-lint
```

Default dev token: `dev-token`

## Branching Model

| Branch pattern       | Purpose                                         |
|---------------------|-------------------------------------------------|
| `main`              | Protected. Always releasable. No direct commits. |
| `feature/<name>`    | Short-lived feature work. Branch from `main`.    |
| `fix/<name>`        | Bug fixes. Branch from `main`.                   |
| `codex/<name>`      | AI-assisted development branches.               |
| `release/<version>` | Stabilization for a release. Created from `main`.|

All PRs target `main`. Squash-merge is preferred for feature branches. Merge commits are used for `release/*` branches.

## Commit Messages

Follow the [Conventional Commits](https://www.conventionalcommits.org/) format:

```
<type>(<scope>): <short summary>

[optional body]
[optional footer]
```

Types: `feat`, `fix`, `refactor`, `test`, `docs`, `chore`, `perf`, `ci`

Scopes: `api`, `mcp`, `storage`, `collector`, `config`, `auth`, `obs`, `providers`, `contracts`, `deploy`

Examples:
```
feat(api): add pagination to readings query
fix(storage): handle concurrent write on WAL mode
docs(contributing): add branching conventions
```

## Pull Request Requirements

Before opening a PR:

- [ ] `go test ./...` passes locally
- [ ] `go vet ./...` clean
- [ ] New code has tests for non-trivial logic
- [ ] CHANGELOG.md updated under `[Unreleased]`
- [ ] No secrets or credentials committed

PR description must include:
- What the change does and why
- How to test it
- Any schema/contract changes flagged explicitly

## Schema and Contract Changes

Changes to `pkg/contracts/` or `docs/openapi.yaml` are **high-impact** and require extra review:

- All changes must be backward-compatible within the same minor version.
- Breaking changes must bump the major version and follow the deprecation policy (minimum 2 minor releases).
- Contract changes must update `docs/schemas/` artifacts.

## Release Process

1. Create a `release/vX.Y.Z` branch from `main`.
2. Update `CHANGELOG.md` — move `[Unreleased]` to the new version section.
3. Bump the version in `go.mod` or any version constants as needed.
4. Open a PR from `release/vX.Y.Z` → `main`. Get review and green CI.
5. Merge and tag: `git tag vX.Y.Z && git push origin vX.Y.Z`.
6. GitHub Actions will build and publish release artifacts.

## Versioning Policy

- **Binaries**: SemVer (`vMAJOR.MINOR.PATCH`)
- **API contracts**: SemVer, tracked separately as `v1alpha1`, `v1beta1`, `v1` etc.
- Patch releases: bug fixes only, no new endpoints.
- Minor releases: additive changes, backward-compatible.
- Major releases: breaking changes, require migration notes.

## Testing

| Target             | Command           | When to run              |
|--------------------|-------------------|--------------------------|
| Unit + integration | `make test`       | Every PR                 |
| Race detector      | `make race`       | Every PR                 |
| Lint               | `make lint`       | Every PR                 |
| Cross-compile      | `make cross`      | Before release           |
| Integration suite  | `make integration`| Before release           |

## Security Vulnerabilities

Do **not** open public issues for security vulnerabilities. Email the maintainers directly. Include:
- Description and impact
- Reproduction steps
- Affected versions

## Code Style

- Standard Go formatting (`gofmt`). No custom linter exceptions without justification.
- Errors must be handled; do not ignore with `_` unless the pattern is intentional and documented.
- No panics in library code paths.
- No global mutable state.
- New packages need a `doc.go` or package-level comment.
