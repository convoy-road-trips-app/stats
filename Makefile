.PHONY: all build test test-race test-cover bench lint clean help

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

# Run linter
lint:
	go vet ./...
	@if command -v golangci-lint >/dev/null; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed. Skipping."; \
	fi

# Clean build artifacts
clean:
	rm -f coverage.out coverage.html
	go clean

# Show help
help:
	@echo "Available targets:"
	@echo "  build       - Build the library"
	@echo "  test        - Run all tests"
	@echo "  test-race   - Run tests with race detector"
	@echo "  test-cover  - Run tests with coverage report"
	@echo "  bench       - Run benchmarks"
	@echo "  lint        - Run linters"
	@echo "  clean       - Clean build artifacts"
