# Contributing

Thanks for your interest in prompt-cleaner.

## Getting started

```bash
git clone https://github.com/scribelia-anthony/prompt-cleaner.git
cd prompt-cleaner
make build
./prompt-cleaner
```

You need Go 1.25 or newer. Everything else (assets, defaults) is
embedded in the binary.

## Development loop

```bash
make run          # build + run with defaults
make test         # unit tests
make test-race    # tests with -race + coverage
make lint         # golangci-lint
make vet          # go vet
make ci           # tidy + vet + test-race
```

Tools assumed on `$PATH` for the optional targets:

- [`golangci-lint`](https://golangci-lint.run/)
- [`govulncheck`](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)
- [`goreleaser`](https://goreleaser.com/) for `make release-snapshot`

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

Everything under `internal/` is unimportable from outside the module on
purpose. If you need to expose something publicly, propose an API in a
PR first.

## Submitting changes

1. Fork & branch from `main`.
2. Keep the change focused. Mixing refactors with feature work makes
   review harder.
3. Add or update tests for any new behaviour.
4. Run `make ci` locally before pushing.
5. Open a PR using the template. CI must pass before review.

### Commit messages

Short imperative summary on the first line, optional body explaining
*why* (not *what* — the diff says what). Examples:

```
fix proxy: don't double-record fallback prefix chunks

Streaming responses that hit the AUP-refusal peek were appending the
same chunks twice when classification finished mid-buffer.
```

```
add -version flag
```

### UI changes

The UI lives in `internal/web/static/`. Changes there are picked up on
the next `go build` thanks to `//go:embed`. There is no separate JS
build step.

## Releases

Releases are cut by tagging `vX.Y.Z` on `main`:

```bash
git tag v0.2.0
git push origin v0.2.0
```

The `release.yml` workflow runs goreleaser, which publishes:

- GitHub release with archives + checksums
- Multi-arch container images on `ghcr.io/scribelia-anthony/prompt-cleaner`

## Reporting bugs

Use the *Bug report* issue template. Include the version
(`prompt-cleaner -version`), the command you ran, and what you observed
vs. what you expected.

## Security issues

See [SECURITY.md](SECURITY.md). Please do not file public issues for
security problems.
