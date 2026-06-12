# Wukong Makefile
# A local-first extensible AI agent platform
#
# Usage:
#   make build        Build the wukong binary
#   make test         Run all unit tests
#   make lint         Run linter (requires golangci-lint)
#   make clean        Remove build artifacts
#   make install      Install wukong to $GOPATH/bin
#   make release      Build release binaries for all platforms
#   make docker-build Build Docker image
#   make help         Show this help message

# Build information
APP_NAME    := wukong
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0")
GIT_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE  := $(shell date -u '+%Y-%m-%d_%H:%M:%S' 2>/dev/null || echo "unknown")
GO          := go
GOFLAGS     := -trimpath
LDFLAGS     := -s -w \
	-X github.com/km269/wukong/internal/cli.Version=$(VERSION) \
	-X github.com/km269/wukong/internal/cli.GitCommit=$(GIT_COMMIT) \
	-X github.com/km269/wukong/internal/cli.BuildDate=$(BUILD_DATE)

# Directories
BUILD_DIR   := build
COVERAGE_DIR:= coverage

# Default target
.DEFAULT_GOAL := help

# =============================================================================
# Development
# =============================================================================

.PHONY: build
build: ## Build the wukong binary for the current platform
	@echo "Building $(APP_NAME) $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" \
		-o $(BUILD_DIR)/$(APP_NAME) ./cmd/wukong/
	@echo "Binary: $(BUILD_DIR)/$(APP_NAME)"

.PHONY: build-all
build-all: ## Build binaries for all supported platforms
	@echo "Building for all platforms..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux   GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-linux-amd64   ./cmd/wukong/
	GOOS=linux   GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-linux-arm64   ./cmd/wukong/
	GOOS=darwin  GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-darwin-amd64  ./cmd/wukong/
	GOOS=darwin  GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-darwin-arm64  ./cmd/wukong/
	GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-windows-amd64.exe ./cmd/wukong/
	@echo "Binaries in $(BUILD_DIR)/"

.PHONY: install
install: ## Install wukong to $GOPATH/bin
	@echo "Installing $(APP_NAME)..."
	$(GO) install $(GOFLAGS) -ldflags "$(LDFLAGS)" ./cmd/wukong/
	@echo "Installed to $$(which $(APP_NAME))"

.PHONY: run
run: build ## Build and run wukong session
	$(BUILD_DIR)/$(APP_NAME) session

# =============================================================================
# Testing
# =============================================================================

.PHONY: test
test: ## Run all unit tests
	@echo "Running unit tests..."
	$(GO) test -race -count=1 -timeout=60s ./...

.PHONY: test-short
test-short: ## Run unit tests (short mode)
	$(GO) test -short -count=1 -timeout=30s ./...

.PHONY: test-verbose
test-verbose: ## Run unit tests with verbose output
	$(GO) test -race -v -count=1 -timeout=60s ./...

.PHONY: test-coverage
test-coverage: ## Run tests with coverage report
	@echo "Running tests with coverage..."
	@mkdir -p $(COVERAGE_DIR)
	$(GO) test -race -count=1 -timeout=120s \
		-coverprofile=$(COVERAGE_DIR)/coverage.out \
		-covermode=atomic ./...
	$(GO) tool cover -html=$(COVERAGE_DIR)/coverage.out \
		-o $(COVERAGE_DIR)/coverage.html
	@echo "Coverage report: $(COVERAGE_DIR)/coverage.html"

.PHONY: test-coverage-func
test-coverage-func: ## Show per-function coverage
	@mkdir -p $(COVERAGE_DIR)
	$(GO) test -race -count=1 -timeout=120s \
		-coverprofile=$(COVERAGE_DIR)/coverage.out ./...
	$(GO) tool cover -func=$(COVERAGE_DIR)/coverage.out

.PHONY: bench
bench: ## Run benchmarks
	$(GO) test -bench=. -benchmem -count=3 ./...

# =============================================================================
# Code Quality
# =============================================================================

.PHONY: fmt
fmt: ## Format code with gofmt
	$(GO) fmt ./...

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: lint
lint: ## Run golangci-lint (requires golangci-lint)
	@which golangci-lint > /dev/null 2>&1 || { \
		echo "golangci-lint not found. Install: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	}
	golangci-lint run --timeout=5m ./...

.PHONY: tidy
tidy: ## Run go mod tidy
	$(GO) mod tidy

.PHONY: verify
verify: ## Run all verification (fmt, vet, test)
	$(MAKE) fmt
	$(MAKE) vet
	$(MAKE) test

# =============================================================================
# Release
# =============================================================================

.PHONY: release
release: build-all ## Build release binaries for all platforms
	@echo "Creating release archives..."
	@mkdir -p $(BUILD_DIR)/release
	for f in $(BUILD_DIR)/$(APP_NAME)-*; do \
		base=$$(basename $$f); \
		ext=""; \
		case $$base in *.exe) ext=".exe" ;; esac; \
		cp $$f $(BUILD_DIR)/release/$(APP_NAME)$$ext; \
		if command -v tar > /dev/null 2>&1; then \
			tar -czf $(BUILD_DIR)/release/$$base.tar.gz -C $(BUILD_DIR) $$base; \
		elif command -v zip > /dev/null 2>&1; then \
			zip -j $(BUILD_DIR)/release/$$base.zip $$f; \
		fi; \
	done
	@echo "Release files in $(BUILD_DIR)/release/"

# =============================================================================
# Docker
# =============================================================================

.PHONY: docker-build
docker-build: ## Build Docker image
	docker build -t $(APP_NAME):$(VERSION) -f Dockerfile .
	docker tag $(APP_NAME):$(VERSION) $(APP_NAME):latest

.PHONY: docker-run
docker-run: ## Run wukong in Docker
	docker run --rm -it \
		-v $(HOME)/.config/wukong:/root/.config/wukong \
		-v $(PWD):/workspace \
		-w /workspace \
		$(APP_NAME):latest session

# =============================================================================
# Cleanup
# =============================================================================

.PHONY: clean
clean: ## Remove build artifacts
	@echo "Cleaning..."
	rm -rf $(BUILD_DIR) $(COVERAGE_DIR)
	$(GO) clean -cache -testcache

.PHONY: clean-all
clean-all: clean ## Remove all generated files
	rm -f wukong.db

# =============================================================================
# Help
# =============================================================================

.PHONY: help
help: ## Show this help message
	@echo "Wukong Makefile"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
