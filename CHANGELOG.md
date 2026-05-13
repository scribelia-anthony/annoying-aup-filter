# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `cmd/` + `internal/` package layout.
- Unit tests for `rules`, `store`, `intercept`, `fallback`, `proxy`.
- GitHub Actions CI (build, vet, race tests, coverage, golangci-lint,
  govulncheck).
- Release pipeline via goreleaser (binary archives + multi-arch
  container images on GHCR).
- `Dockerfile` (local) and `Dockerfile.release` (distroless, multi-arch).
- `Makefile` with `build`, `test`, `lint`, `vuln`, `docker`,
  `release-snapshot`, etc.
- `LICENSE` (MIT), `SECURITY.md`, `CONTRIBUTING.md`, `.editorconfig`,
  `.golangci.yml`, `.dockerignore`.
- Issue templates (bug, feature) and pull request template.
- `-version` flag.
- HTTP `ReadHeaderTimeout` on both servers to defuse slowloris-style
  abuse if the listeners ever escape `127.0.0.1`.

### Changed
- Go module path is now `github.com/scribelia-anthony/prompt-cleaner`.

## [0.1.0] — 2025-05-13

### Added
- Initial public release. Burp-like HTTP proxy + web UI for the
  Anthropic Messages API: capture, intercept, edit, replay, match-and-
  replace rules, AUP-refusal fallback.
