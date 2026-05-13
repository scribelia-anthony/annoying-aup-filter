# prompt-cleaner

A single-binary HTTP proxy + web UI that sits between Claude Code (or any
Anthropic SDK) and `api.anthropic.com` — like a tiny Burp Suite, just for
Claude.

Capture, inspect, modify, and replay every Claude API call.

## Features

- **Transparent proxy** — point `ANTHROPIC_BASE_URL` at it, forwards
  everything to the real Anthropic API (or any upstream of your choice).
- **Streaming-aware** — handles `text/event-stream` responses chunk by
  chunk; chunks are forwarded to the client immediately and captured for
  later inspection.
- **Live web UI** — dark-themed, no framework. Captures stream in via
  Server-Sent Events; click any request to see headers and body, with
  JSON / SSE syntax highlighting.
- **Intercept mode** — toggle ON to pause every request before it leaves
  the box. Edit URL, headers, body, then Forward (modified or unchanged)
  or Drop.
- **Match & Replace rules** — regex rewrites applied automatically to
  URL, headers, or body on the request or response side.
- **Replay** — clone any captured request, edit, re-send.

## Build & run

```bash
go build -o prompt-cleaner ./...
./prompt-cleaner
```

Default flags:

| Flag             | Default                       | Meaning                                |
|------------------|-------------------------------|----------------------------------------|
| `-proxy-addr`    | `127.0.0.1:8080`              | where the proxy listens                |
| `-ui-addr`       | `127.0.0.1:8888`              | where the UI + admin API listen        |
| `-upstream`      | `https://api.anthropic.com`   | where requests are forwarded           |
| `-max-captures`  | `1000`                        | ring-buffer size for in-memory history |

## Use it with Claude Code

In the shell where you launch Claude Code:

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
claude
```

Then open <http://127.0.0.1:8888> in your browser. Every request from
Claude Code (and the streaming SSE response) will appear live in the UI.

## Admin REST API

The UI itself uses these; you can also script against them.

| Method | Path                                    | Body / effect                                       |
|--------|-----------------------------------------|-----------------------------------------------------|
| GET    | `/admin/state`                          | snapshot of intercept + rules + upstream            |
| GET    | `/admin/captures`                       | list all captures                                   |
| GET    | `/admin/captures/{id}`                  | one capture, full detail                            |
| POST   | `/admin/captures/{id}/forward`          | release an intercepted request (body: `{url,headers,body}`) |
| POST   | `/admin/captures/{id}/drop`             | drop an intercepted request                         |
| POST   | `/admin/captures/{id}/replay`           | clone + send (body: `{method,url,headers,body}`)    |
| POST   | `/admin/intercept`                      | `{ "enabled": true }`                               |
| GET    | `/admin/intercept`                      | current intercept state + pending ids               |
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
  "match": "haiku",                // Go regex syntax (RE2)
  "replacement": "sonnet"          // may use $1, $2 …
}
```

## Caveats

- The proxy talks plain HTTP to Claude Code (no TLS termination needed
  since it's localhost). It uses HTTPS when forwarding upstream.
- Request bodies and SSE events are stored in memory as strings —
  non-text payloads are stored as raw bytes interpreted as a string.
  Fine for the Anthropic Messages API; do not use for binary uploads.
- Response-body rules in streaming mode run **per chunk**, so a regex
  that spans a chunk boundary will miss.
- Auth tokens (`x-api-key`, `Authorization`) are stored verbatim in the
  capture log. Treat the UI port as sensitive — bind to `127.0.0.1` only
  (the default).

## Layout

```
prompt-cleaner/
├── main.go         # flags + two HTTP servers (proxy + UI)
├── proxy.go        # forwarding, streaming, hop-by-hop handling
├── intercept.go    # pause / decide channel per pending request
├── rules.go        # regex match-and-replace engine
├── store.go        # in-memory ring buffer + event broadcaster
├── api.go          # admin REST + SSE /events stream
└── static/         # UI assets, embedded into the binary
```
