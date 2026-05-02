SHELL := /usr/bin/env bash

GO        ?= go
GOFLAGS   ?=
LDFLAGS   ?= -s -w \
             -X github.com/teo-dev/teo/internal/version.Version=$(VERSION) \
             -X github.com/teo-dev/teo/internal/version.Commit=$(COMMIT) \
             -X github.com/teo-dev/teo/internal/version.Date=$(DATE)

VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE      ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

BIN_DIR   := bin
SERVICES  := teo api run-manager scheduler result-pipeline predictor worker

.PHONY: all
all: lint test build

.PHONY: build
build: $(addprefix $(BIN_DIR)/,$(SERVICES))

$(BIN_DIR)/%: $(shell find cmd internal pkg -name '*.go' 2>/dev/null) go.mod
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $@ ./cmd/$*

.PHONY: test
test:
	$(GO) test -race -count=1 -timeout 120s ./...

.PHONY: test-short
test-short:
	$(GO) test -short -race -count=1 -timeout 60s ./...

.PHONY: test-integration
test-integration:
	@which docker > /dev/null || { echo "docker required for integration tests"; exit 1; }
	$(GO) test -tags=integration -race -count=1 -timeout 10m ./...

.PHONY: coverage
coverage:
	$(GO) test -race -count=1 -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "coverage report: coverage.html"

.PHONY: lint
lint:
	@which golangci-lint > /dev/null || { echo "golangci-lint not installed; see https://golangci-lint.run/"; exit 1; }
	golangci-lint run ./...

.PHONY: fmt
fmt:
	$(GO) fmt ./...
	@which goimports > /dev/null && goimports -w -local github.com/teo-dev/teo . || true

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: tidy
tidy:
	$(GO) mod tidy

.PHONY: licenses
licenses:
	@which go-licenses > /dev/null || { echo "go-licenses not installed; run: go install github.com/google/go-licenses@latest"; exit 1; }
	go-licenses check ./... \
	  --disallowed_types=forbidden,restricted

# --- Protobuf --------------------------------------------------------------

# Regenerates internal/proto/teov1/*.pb.go from the .proto files via buf.
# Requires:
#   - buf                 (`go install github.com/bufbuild/buf/cmd/buf@latest`)
#   - protoc-gen-go       (`go install google.golang.org/protobuf/cmd/protoc-gen-go@latest`)
#   - protoc-gen-go-grpc  (`go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest`)
.PHONY: proto
proto:
	cd proto && buf generate

# --- Migrations (placeholder; populated in E-02) ---------------------------

.PHONY: migrate
migrate:
	@echo "migrations will be wired up in E-02; no-op for now"

# --- Helm ------------------------------------------------------------------

.PHONY: helm-lint
helm-lint:
	@which helm > /dev/null || { echo "helm not installed"; exit 1; }
	@if [ -d deploy/helm/teo ]; then helm lint deploy/helm/teo; else echo "deploy/helm/teo not yet created (E-11)"; fi

.PHONY: helm-template
helm-template:
	@if [ -d deploy/helm/teo ]; then helm template teo deploy/helm/teo; else echo "deploy/helm/teo not yet created (E-11)"; fi

# --- Docker ----------------------------------------------------------------

DOCKER_REGISTRY ?= ghcr.io/teo-dev
DOCKER_TAG      ?= $(VERSION)

.PHONY: docker-build
docker-build:
	@for svc in $(SERVICES); do \
	  echo "==> docker build $$svc"; \
	  docker build \
	    --build-arg SERVICE=$$svc \
	    --build-arg VERSION=$(VERSION) \
	    --build-arg COMMIT=$(COMMIT) \
	    --build-arg DATE=$(DATE) \
	    -t $(DOCKER_REGISTRY)/$$svc:$(DOCKER_TAG) \
	    -f Dockerfile . ; \
	done

# --- Cleanup ---------------------------------------------------------------

.PHONY: clean
clean:
	rm -rf $(BIN_DIR) coverage.out coverage.html

.PHONY: help
help:
	@echo "TEO build targets:"
	@echo "  make build         - compile all service binaries into bin/"
	@echo "  make test          - run unit + integration tests"
	@echo "  make test-short    - run only short tests"
	@echo "  make coverage      - run tests and produce coverage.html"
	@echo "  make lint          - run golangci-lint"
	@echo "  make fmt           - gofmt + goimports"
	@echo "  make vet           - go vet"
	@echo "  make tidy          - go mod tidy"
	@echo "  make licenses      - check transitive license compatibility"
	@echo "  make helm-lint     - lint the Helm chart"
	@echo "  make docker-build  - build all service images"
	@echo "  make clean         - remove build artifacts"
