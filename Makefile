GO ?= go
# Build like CI: against published modules, never a local go.work.
export GOWORK ?= off

PKG := github.com/steermi/cli/internal/buildinfo
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X $(PKG).version=$(VERSION) -X $(PKG).commit=$(COMMIT) -X $(PKG).date=$(DATE)

.PHONY: check fmt vet test race tidy vuln docs build install licenses snapshot

check: fmt vet test race tidy docs

fmt:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	$(GO) vet ./...

test:
	$(GO) test ./... -count=1

race:
	$(GO) test -race ./... -count=1

tidy:
	$(GO) mod tidy -diff
	$(GO) mod verify

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

docs:
	python3 scripts/check-repo.py .

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/steermi ./cmd/steermi

install:
	CGO_ENABLED=0 $(GO) install -trimpath -ldflags "$(LDFLAGS)" ./cmd/steermi

# Collects the license texts that release archives ship.
licenses:
	$(GO) run github.com/google/go-licenses/v2@v2.0.1 save ./cmd/steermi --save_path=build/licenses --force --ignore github.com/steermi

# Builds every release archive locally without publishing anything.
snapshot:
	goreleaser release --snapshot --clean
