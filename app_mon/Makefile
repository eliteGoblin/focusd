.PHONY: all build build-release test test-unit test-integration clean install lint deps run fmt

# Build variables
BINARY_NAME=appmon
BUILD_DIR=./build
GO_FILES=$(shell find . -name '*.go' -not -path './vendor/*')

# Version info
VERSION?=0.1.0
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(BUILD_TIME)"

# Default target
all: deps lint test build

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

# Build binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/appmon

# Build optimized release binary
build-release:
	@echo "Building release binary..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build $(LDFLAGS) -a -installsuffix cgo -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/appmon

# Build for macOS ARM64 (Apple Silicon)
build-mac-arm64:
	@echo "Building for macOS ARM64..."
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/appmon

# Build for macOS AMD64 (Intel)
build-mac-amd64:
	@echo "Building for macOS AMD64..."
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/appmon

# Run all tests
test: test-unit

# Run unit tests
test-unit:
	@echo "Running unit tests..."
	go test -v -race -coverprofile=coverage-unit.out ./internal/...

# Run integration tests
test-integration:
	@echo "Running integration tests..."
	go test -v -race -tags=integration -coverprofile=coverage-integration.out ./test/integration/...

# Generate coverage report
coverage:
	@echo "Generating coverage report..."
	go tool cover -html=coverage-unit.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Run linters
lint:
	@echo "Running linters..."
	@which golangci-lint > /dev/null || (echo "golangci-lint not found, skipping lint" && exit 0)
	golangci-lint run ./...

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf $(BUILD_DIR)
	rm -f coverage*.out coverage.html

# Install to /usr/local/bin
install: build
	@echo "Installing $(BINARY_NAME) to /usr/local/bin..."
	sudo cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/
	sudo chmod +x /usr/local/bin/$(BINARY_NAME)
	@echo "Installation complete!"
	@echo "Run 'appmon start' to begin protection."

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...
	@echo "Code formatted."

# Run in development mode
run:
	go run ./cmd/appmon $(ARGS)

# Development shortcuts
dev-start:
	go run ./cmd/appmon start

dev-status:
	go run ./cmd/appmon status

dev-list:
	go run ./cmd/appmon list

# Show help
help:
	@echo "appmon Makefile targets:"
	@echo ""
	@echo "  make build          - Build the binary"
	@echo "  make build-release  - Build optimized release binary"
	@echo "  make test           - Run all tests"
	@echo "  make test-unit      - Run unit tests"
	@echo "  make test-integration - Run integration tests"
	@echo "  make coverage       - Generate coverage report"
	@echo "  make lint           - Run linters"
	@echo "  make fmt            - Format code"
	@echo "  make install        - Install to /usr/local/bin"
	@echo "  make clean          - Remove build artifacts"
	@echo "  make deps           - Download dependencies"
	@echo ""
	@echo "NOTE: There is no uninstall command. This is intentional."
	@echo ""
