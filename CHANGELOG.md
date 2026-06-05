# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] — 2026-06-05

First public release. A single-binary HTTP proxy + web UI that sits between
Claude Code (or any Anthropic SDK) and the Anthropic API.

### Features

- Transparent, streaming-aware proxy with a live dark-theme web UI (SSE).
- Capture, inspect, intercept/edit, and replay every request and response.
- Regex match-and-replace rules on URL, headers, or body, in either direction.
- AUP-refusal fallback: when the newest model refuses mid-task, transparently
  retry the same request on an older model (Opus 4.6 by default) that rarely
  trips the classifier — so in-progress work isn't lost.
- Optional on-disk persistence of rules, fallback, and intercept state via
  `-rules-file`.

### Packaging

- Prebuilt binaries (linux / darwin / windows, amd64 / arm64) and multi-arch
  container images on GHCR, cut by goreleaser.
- GitHub Actions CI: build, vet, race tests + coverage, golangci-lint,
  govulncheck.

_Previously developed under the name `prompt-cleaner`._
