# prompt-cleaner

[![CI](https://github.com/scribelia-anthony/prompt-cleaner/actions/workflows/ci.yml/badge.svg)](https://github.com/scribelia-anthony/prompt-cleaner/actions/workflows/ci.yml)
[![Release](https://github.com/scribelia-anthony/prompt-cleaner/actions/workflows/release.yml/badge.svg)](https://github.com/scribelia-anthony/prompt-cleaner/actions/workflows/release.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/scribelia-anthony/prompt-cleaner.svg)](https://pkg.go.dev/github.com/scribelia-anthony/prompt-cleaner)
[![Go Report Card](https://goreportcard.com/badge/github.com/scribelia-anthony/prompt-cleaner)](https://goreportcard.com/report/github.com/scribelia-anthony/prompt-cleaner)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A single-binary HTTP proxy + web UI that sits between Claude Code (or
any Anthropic SDK) and `api.anthropic.com` — like a tiny Burp Suite,
just for Claude.

Capture, inspect, modify, intercept, and replay every Claude API call.

## Features

- **Transparent proxy** — point `ANTHROPIC_BASE_URL` at it; forwards
  everything to the real Anthropic API (or any upstream of your choice).
- **Streaming-aware** — `text/event-stream` responses are forwarded
  chunk by chunk while being captured for later inspection.
- **Live web UI** — dark theme, no framework. Captures stream in via
  Server-Sent Events; click a request to see headers and body with
  JSON / SSE syntax highlighting.
- **Intercept mode** — pause every request before it leaves the host.
  Edit URL, headers, body, then forward (modified or unchanged) or drop.
- **Match-and-replace rules** — regex rewrites applied automatically to
  URL, headers, or body on either side of the wire.
- **AUP-refusal fallback** — detect early SSE `stop_reason: "refusal"`
  responses and transparently retry against a fallback model.
- **Replay** — clone any captured request, edit, re-send.

## Install

### Binary release

Download from the [releases page](https://github.com/scribelia-anthony/prompt-cleaner/releases),
or via `go install`:

```bash
go install github.com/scribelia-anthony/prompt-cleaner/cmd/prompt-cleaner@latest
```

### Container

```bash
docker run --rm -p 8080:8080 -p 8888:8888 \
  ghcr.io/scribelia-anthony/prompt-cleaner:latest
```

### From source

```bash
git clone https://github.com/scribelia-anthony/prompt-cleaner.git
cd prompt-cleaner
make build
./prompt-cleaner
```

Go 1.25+ required.

## Use it with Claude Code

In the shell where you launch Claude Code:

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
claude
```

Then open <http://127.0.0.1:8888> in your browser. Every request from
Claude Code (and the streaming SSE response) appears live in the UI.

## Flags

| Flag             | Default                       | Meaning                                |
|------------------|-------------------------------|----------------------------------------|
| `-proxy-addr`    | `127.0.0.1:8080`              | where the proxy listens                |
| `-ui-addr`       | `127.0.0.1:8888`              | where the UI + admin API listen        |
| `-upstream`      | `https://api.anthropic.com`   | where requests are forwarded           |
| `-max-captures`  | `1000`                        | ring-buffer size for in-memory history |
| `-rules-file`    | (empty)                       | JSON file of rules to load at startup  |
| `-version`       | —                             | print version info and exit            |

## Admin REST API

The UI itself uses these; you can also script against them.

| Method | Path                                    | Effect                                              |
|--------|-----------------------------------------|-----------------------------------------------------|
| GET    | `/admin/state`                          | snapshot of intercept + rules + upstream            |
| GET    | `/admin/captures`                       | list all captures                                   |
| GET    | `/admin/captures/{id}`                  | one capture, full detail                            |
| POST   | `/admin/captures/{id}/forward`          | release an intercepted request                      |
| POST   | `/admin/captures/{id}/drop`             | drop an intercepted request                         |
| POST   | `/admin/captures/{id}/replay`           | clone + send                                        |
| POST   | `/admin/intercept`                      | `{ "enabled": bool }`                               |
| GET    | `/admin/intercept`                      | current intercept state + pending ids               |
| GET    | `/admin/fallback`                       | current AUP fallback state                          |
| POST   | `/admin/fallback`                       | `{ "enabled": bool, "model": "..." }`               |
| GET    | `/admin/rules`                          | list rules                                          |
| PUT    | `/admin/rules`                          | replace all rules (body: `[{rule}, …]`)             |
| POST   | `/admin/clear`                          | wipe captures                                       |
| GET    | `/events`                               | SSE event stream consumed by the UI                 |

### Rule shape

```jsonc
{
  "name": "rewrite-model",
  "enabled": true,
  "phase": "request",              // "request" | "response"
  "target": "body",                // "url" | "header" | "body"
  "header_name": "X-Api-Key",      // only when target == "header"
  "match": "haiku",                // RE2 regex
  "replacement": "sonnet"          // may use $1, $2 …
}
```

## Layout

```
cmd/prompt-cleaner/   binary entry point (flags + boot)
internal/api/         admin REST + SSE handler
internal/fallback/    AUP-refusal fallback policy
internal/id/          short id generator
internal/intercept/   pause / forward / drop gate
internal/proxy/       HTTP forwarder, streaming, fallback peek
internal/rules/       regex match & replace engine
internal/store/       in-memory ring buffer + event broadcaster
internal/web/         embedded UI assets (HTML/CSS/JS)
```

## Caveats

- The proxy talks plain HTTP to clients (no TLS termination needed for
  localhost). It uses HTTPS when forwarding upstream.
- Request bodies and SSE events are stored in memory as strings. Fine
  for the Anthropic Messages API; do not use for binary uploads.
- Response-body rules in streaming mode run **per chunk**, so a regex
  that spans a chunk boundary will miss.
- Auth tokens (`x-api-key`, `Authorization`) are stored verbatim in the
  capture log. Treat the UI port as sensitive — keep it bound to
  `127.0.0.1`. See [SECURITY.md](SECURITY.md) for the threat model.

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for the dev loop.

```bash
make help        # list all targets
make ci          # tidy + vet + race tests
```

## License

[MIT](LICENSE).
