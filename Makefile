# Penguintier developer commands. Run `make` or `make help` to list them.
#
# The Go code lives in the root module (no cgo). ui/ is a separate Go module
# (Wails needs cgo + webkit), so the ui-* targets run inside ui/.
# Targets marked TODO(Px) are filled in by that phase.

GO ?= go
# Where `go install` puts binaries: $GOBIN if set, else $GOPATH/bin (~/go/bin).
GOBIN := $(or $(shell $(GO) env GOBIN),$(shell $(GO) env GOPATH)/bin)

# Keep in sync with the golangci-lint version in .github/workflows/ci.yml.
GOLANGCI_LINT_VERSION := v2.15.0
GOLANGCI_LINT ?= $(GOBIN)/golangci-lint
WAILS ?= $(GOBIN)/wails

# The Go version pinned by the toolchain line in go.mod, e.g. go1.27.2.
GO_TOOLCHAIN := $(shell $(GO) env GOVERSION)

# Dependency licenses that fit an MIT project (checked by `make licenses`).
ALLOWED_LICENSES := MIT,BSD-2-Clause,BSD-3-Clause,Apache-2.0,ISC,MPL-2.0

# Version stamped into binaries (see "Version" in docs/architecture.md).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Wails must build against webkit2gtk-4.1 (Ubuntu 24.04+, Debian 12+, Fedora).
UI_TAGS := webkit2_41
# Which window `make ui-dev` opens: join or about.
UI_SCREEN ?= join

# Extra arguments for `make mock`, e.g. make mock MOCK_ARGS="--help".
MOCK_ARGS ?=

.DEFAULT_GOAL := help
.PHONY: help tools build test lint fmt vuln licenses ui-dev ui-build run mock tray-mock live-test dev-install-helper package clean

help: ## List the targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  %-20s %s\n", $$1, $$2}'

tools: ## Install the pinned dev tools (golangci-lint, wails) into ~/go/bin
	# Build the tools with the project's Go: golangci-lint refuses to check
	# code that targets a newer Go than the one it was built with.
	GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	# The Wails CLI must match the Wails library version in ui/go.mod.
	cd ui && GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) install github.com/wailsapp/wails/v2/cmd/wails@$$($(GO) list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)

build: ## Build all Go commands into bin/ (static, no cgo)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/ ./cmd/...

test: ## Run the unit tests with the race detector
	$(GO) test -race ./...

lint: ## Check formatting, then run go vet and golangci-lint
	@out=$$(gofmt -l cmd internal ui/*.go); \
	if [ -n "$$out" ]; then echo "These files need gofmt (run 'make fmt'):"; echo "$$out"; exit 1; fi
	$(GO) vet ./...
	@test -x $(GOLANGCI_LINT) || { echo "golangci-lint not found; run 'make tools'"; exit 1; }
	$(GOLANGCI_LINT) run

fmt: ## Format all Go code (gofmt + goimports)
	@test -x $(GOLANGCI_LINT) || { echo "golangci-lint not found; run 'make tools'"; exit 1; }
	$(GOLANGCI_LINT) fmt
	gofmt -w ui/*.go

# `go run tool@version` would otherwise build the tool with a different Go
# than the pinned one, and the tool then misreads the standard library.
vuln: ## Check for known vulnerabilities (govulncheck)
	GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

licenses: ## Check that every dependency has an allowed license
	GOTOOLCHAIN=$(GO_TOOLCHAIN) GOROOT=$$($(GO) env GOROOT) \
		$(GO) run github.com/google/go-licenses/v2@v2.0.1 check ./... --allowed_licenses=$(ALLOWED_LICENSES)

ui-dev: ## Open a UI window with hot reload (UI_SCREEN=join or about)
	@test -x $(WAILS) || { echo "wails not found; run 'make tools'"; exit 1; }
	# WAILS_DEV_ALLOWED_HOSTS: the dev window loads wails://wails.localhost:34115,
	# and Wails 2.17's dev server rejects that host name unless it is listed,
	# which breaks hot reload. *.localhost always means this machine.
	cd ui && WEBKIT_DISABLE_DMABUF_RENDERER=1 WAILS_DEV_ALLOWED_HOSTS=wails.localhost \
		$(WAILS) dev -tags $(UI_TAGS) -appargs $(UI_SCREEN)

ui-build: ## Build the UI binary into ui/build/bin/penguintier-ui
	@test -x $(WAILS) || { echo "wails not found; run 'make tools'"; exit 1; }
	cd ui && $(WAILS) build -tags $(UI_TAGS) -trimpath -ldflags '-X main.version=$(VERSION)'

run: ## Run the tray from source
	$(GO) run ./cmd/penguintier

mock: ## Run zt-mock, the fake ZeroTier API (MOCK_ARGS=...)
	$(GO) run ./cmd/zt-mock $(MOCK_ARGS)

tray-mock: ## TODO(P3): run the tray against zt-mock
	@echo "TODO(P3): run the tray against zt-mock"; exit 1

live-test: ## Run the live tests against the real local ZeroTier service
	$(GO) test -tags live -count=1 ./...

# pkexec only shows our own prompt when the helper sits at the exact path
# named in the policy (exec.path), so it has to be installed system-wide.
# polkit notices new .policy files by itself; no restart is needed.
dev-install-helper: ## Install the token helper + polkit policy for local testing (asks for sudo)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/penguintier-token-helper ./cmd/penguintier-token-helper
	sudo install -D -m 0755 -o root -g root bin/penguintier-token-helper /usr/libexec/penguintier/penguintier-token-helper
	sudo install -D -m 0644 -o root -g root packaging/polkit/io.github.rasikhq.Penguintier.policy /usr/share/polkit-1/actions/io.github.rasikhq.Penguintier.policy
	@echo "Installed. Try: pkexec /usr/libexec/penguintier/penguintier-token-helper"

package: ## TODO(P6): build .deb and .rpm packages into dist/ with GoReleaser
	@echo "TODO(P6): goreleaser release --snapshot --clean"; exit 1

clean: ## Delete build output
	rm -rf bin/ dist/ ui/build/bin/
