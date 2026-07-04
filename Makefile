# Project:Nova — Makefile
# Build, package, and develop Project:Nova locally and for Windows.

# ---- Configuration ----------------------------------------------------------
BINARY_NAME    ?= nova
VERSION        ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.1-dev")
COMMIT         ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE     ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS        := -s -w \
	-X "github.com/project-nova/nova/internal/version.Version=$(VERSION)" \
	-X "github.com/project-nova/nova/internal/version.Commit=$(COMMIT)" \
	-X "github.com/project-nova/nova/internal/version.BuildDate=$(BUILD_DATE)"

GO             ?= go
GOFLAGS        ?= -trimpath
LDFLAGS_GO     := $(GOFLAGS) -ldflags "$(LDFLAGS)"

# Windows-specific
WIN_ARCH       ?= amd64
WIN_LDFLAGS    := $(GOFLAGS) -ldflags "$(LDFLAGS) -H=windowsgui"

# Output
DIST_DIR       := dist

# ---- Phony targets ----------------------------------------------------------
.PHONY: all build build-windows build-cli build-tray test lint fmt vet tidy clean install run serve help package-msi package-zip

all: build

## help: Print this help.
help:
	@awk 'BEGIN {FS = ":.*##"; printf "Project:Nova — Makefile\n\nUsage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} \
	/^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

## build: Build the CLI for the current platform.
build: build-cli
	@echo "✓ Built $(BINARY_NAME) for $$(go env GOOS)/$$(go env GOARCH)"

## build-cli: Build the nova CLI binary.
build-cli:
	$(GO) build $(LDFLAGS_GO) -o $(BINARY_NAME) ./cmd/nova

## build-windows: Cross-compile a Windows .exe (console + tray-capable).
build-windows:
	@mkdir -p $(DIST_DIR)
	GOOS=windows GOARCH=$(WIN_ARCH) $(GO) build $(WIN_LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME).exe ./cmd/nova
	@echo "✓ Built $(DIST_DIR)/$(BINARY_NAME).exe (Windows $(WIN_ARCH))"

## build-tray: Build the desktop tray launcher (Windows). Same binary, GUI subsystem.
build-tray: build-windows
	@echo "✓ Tray-capable build at $(DIST_DIR)/$(BINARY_NAME).exe"

## package-msi: Build a Windows MSI installer (requires WiX Toolset on Windows or wine+WiX).
package-msi: build-windows
	@echo "→ Packaging MSI (run scripts/build-msi.ps1 on Windows)..."
	@powershell -ExecutionPolicy Bypass -File scripts/build-msi.ps1 -Version $(VERSION) 2>/dev/null || \
		echo "  (skipped: PowerShell/WiX not available on this host — run on Windows)"

## package-zip: Zip the Windows build for portable distribution.
package-zip: build-windows
	@cd $(DIST_DIR) && zip -j $(BINARY_NAME)-windows-$(WIN_ARCH)-$(VERSION).zip $(BINARY_NAME).exe
	@echo "✓ Packaged $(DIST_DIR)/$(BINARY_NAME)-windows-$(WIN_ARCH)-$(VERSION).zip"

## run: Run the API server locally (equivalent to `nova serve`).
run: serve

## serve: Start the Nova API server.
serve: build
	./$(BINARY_NAME) serve

## test: Run unit tests.
test:
	$(GO) test ./... -race -count=1

## lint: Run golangci-lint (if installed) else go vet.
lint: vet
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || echo "(golangci-lint not installed; ran go vet)"

## vet: Run go vet.
vet:
	$(GO) vet ./...

## fmt: Format Go source.
fmt:
	$(GO) fmt ./...
	@command -v goimports >/dev/null 2>&1 && goimports -w . || true

## tidy: Tidy module dependencies.
tidy:
	$(GO) mod tidy

## install: Install the nova binary to $$GOBIN / $$GOPATH/bin.
install:
	$(GO) install $(LDFLAGS_GO) ./cmd/nova

## clean: Remove build artifacts.
clean:
	rm -rf $(BINARY_NAME) $(BINARY_NAME).exe $(DIST_DIR) build wix standalone
	@echo "✓ Cleaned build artifacts"
