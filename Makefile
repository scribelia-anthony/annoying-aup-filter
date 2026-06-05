.DEFAULT_GOAL := help

BIN        := annoying-aup-filter
PKG        := github.com/scribelia-anthony/annoying-aup-filter
CMD        := ./cmd/annoying-aup-filter
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE       ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
GOFLAGS    := -trimpath
COVERPROFILE := coverage.out

.PHONY: help
help: ## List available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_.-]+:.*## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build the binary into ./$(BIN)
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN) $(CMD)

.PHONY: install
install: ## go install the binary into $GOBIN
	go install $(GOFLAGS) -ldflags '$(LDFLAGS)' $(CMD)

.PHONY: run
run: ## Build + run with default flags
	go run $(CMD)

.PHONY: test
test: ## Run unit tests
	go test ./...

.PHONY: test-race
test-race: ## Run tests with -race + coverage
	go test -race -coverprofile=$(COVERPROFILE) -covermode=atomic ./...

.PHONY: cover
cover: test-race ## Generate HTML coverage report
	go tool cover -html=$(COVERPROFILE)

.PHONY: vet
vet: ## go vet
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint (must be installed)
	golangci-lint run ./...

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: fmt
fmt: ## gofmt + goimports
	gofmt -s -w .
	@command -v goimports >/dev/null && goimports -w -local $(PKG) . || true

.PHONY: vuln
vuln: ## Run govulncheck (must be installed)
	govulncheck ./...

.PHONY: docker
docker: ## Build local docker image
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		-t $(BIN):$(VERSION) \
		-t $(BIN):latest .

.PHONY: release-snapshot
release-snapshot: ## Build a local snapshot release with goreleaser
	goreleaser release --snapshot --clean

.PHONY: clean
clean: ## Remove build artefacts
	rm -f $(BIN) $(COVERPROFILE)
	rm -rf dist

.PHONY: ci
ci: tidy vet test-race ## Run the full local CI sequence
