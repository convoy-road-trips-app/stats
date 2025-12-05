.PHONY: all build test test-race test-cover bench lint clean help \
        integration-up integration-down integration-test integration-test-verbose integration-logs \
        install-golangci-lint

# Tool versions
GOLANGCI_LINT_VERSION := v2.7.0

# Default target
all: test

# Build the library
build:
	go build ./...

# Run all tests
test:
	go test -v ./...

# Run tests with race detector
test-race:
	go test -v -race ./...

# Run tests with coverage
test-cover:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Run benchmarks
bench:
	go test -bench=. -benchmem ./...

# Install golangci-lint with specified version
install-golangci-lint:
	@echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION)..."
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(shell go env GOPATH)/bin $(GOLANGCI_LINT_VERSION)
	@echo "golangci-lint $(GOLANGCI_LINT_VERSION) installed successfully"

# Run linter (installs golangci-lint if not present)
lint:
	go vet ./...
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "golangci-lint not found. Installing $(GOLANGCI_LINT_VERSION)..."; \
		$(MAKE) install-golangci-lint; \
	fi
	@echo "Running golangci-lint..."
	@golangci-lint run

# Start Docker services for integration tests
integration-up:
	@echo "Starting Docker services for integration tests..."
	cd test/integration && docker compose up -d
	@echo "Waiting for services to be healthy..."
	@sleep 10

# Stop Docker services
integration-down:
	@echo "Stopping Docker services..."
	cd test/integration && docker compose down -v

# Run integration tests
integration-test: integration-up
	@echo "Running integration tests..."
	go test -v -tags=integration ./test/integration/... || (make integration-down && exit 1)
	@make integration-down

# Run integration tests with verbose output
integration-test-verbose: integration-up
	@echo "Running integration tests (verbose)..."
	go test -v -tags=integration ./test/integration/... -test.v || (make integration-down && exit 1)
	@make integration-down

# View logs from Docker services
integration-logs:
	cd test/integration && docker-compose logs -f

# Clean build artifacts
clean:
	rm -f coverage.out coverage.html
	go clean

# Show help
help:
	@echo "Available targets:"
	@echo "  build                    - Build the library"
	@echo "  test                     - Run all tests"
	@echo "  test-race                - Run tests with race detector"
	@echo "  test-cover               - Run tests with coverage report"
	@echo "  bench                    - Run benchmarks"
	@echo "  lint                     - Run linters (auto-installs golangci-lint if needed)"
	@echo "  install-golangci-lint    - Install golangci-lint $(GOLANGCI_LINT_VERSION)"
	@echo "  integration-up           - Start Docker services"
	@echo "  integration-down         - Stop Docker services"
	@echo "  integration-test         - Run integration tests"
	@echo "  integration-test-verbose - Run integration tests (verbose)"
	@echo "  integration-logs         - View Docker service logs"
	@echo "  clean                    - Clean build artifacts"
