.PHONY: all build clean test lint install release

# Build variables
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS = -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"

# Directories
BUILD_DIR = ./bin
CMD_DIR = ./cmd/proxmox-guardian

# Go commands
GO = go
GOFMT = gofmt
GOLINT = golangci-lint

all: lint test build

# Build the binary
build:
	@echo "Building proxmox-guardian $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/proxmox-guardian $(CMD_DIR)

# Build for multiple platforms
build-all: build-linux-amd64 build-linux-arm64

build-linux-amd64:
	@echo "Building for linux/amd64..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/proxmox-guardian-linux-amd64 $(CMD_DIR)

build-linux-arm64:
	@echo "Building for linux/arm64..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/proxmox-guardian-linux-arm64 $(CMD_DIR)

# Run tests
test:
	@echo "Running tests..."
	$(GO) test -v -race -coverprofile=coverage.out ./...

# Run tests with coverage report
test-coverage: test
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Lint the code
lint:
	@echo "Running linter..."
	$(GOLINT) run ./...

# Format the code
fmt:
	@echo "Formatting code..."
	$(GOFMT) -s -w .

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -f coverage.out coverage.html

# Install locally
install: build
	@echo "Installing to /usr/local/bin..."
	@sudo cp $(BUILD_DIR)/proxmox-guardian /usr/local/bin/
	@sudo chmod +x /usr/local/bin/proxmox-guardian

# Install systemd service
install-service: install
	@echo "Installing systemd service..."
	@sudo mkdir -p /etc/proxmox-guardian
	@sudo mkdir -p /var/lib/proxmox-guardian
	@sudo mkdir -p /var/log/proxmox-guardian
	@sudo cp configs/guardian.yaml.example /etc/proxmox-guardian/guardian.yaml.example
	@sudo cp configs/secrets.yaml.example /etc/proxmox-guardian/secrets.yaml.example
	@sudo cp systemd/proxmox-guardian.service /etc/systemd/system/
	@sudo systemctl daemon-reload
	@echo ""
	@echo "Service installed. Next steps:"
	@echo "  1. Edit /etc/proxmox-guardian/guardian.yaml"
	@echo "  2. Edit /etc/proxmox-guardian/secrets.yaml (chmod 600)"
	@echo "  3. sudo systemctl enable proxmox-guardian"
	@echo "  4. sudo systemctl start proxmox-guardian"

# Uninstall
uninstall:
	@echo "Uninstalling..."
	@sudo systemctl stop proxmox-guardian || true
	@sudo systemctl disable proxmox-guardian || true
	@sudo rm -f /etc/systemd/system/proxmox-guardian.service
	@sudo rm -f /usr/local/bin/proxmox-guardian
	@sudo systemctl daemon-reload
	@echo "Uninstalled. Config files in /etc/proxmox-guardian preserved."

# Development: run locally
dev:
	$(GO) run $(CMD_DIR) --config configs/guardian.yaml.example validate

# Development: watch for changes
watch:
	@which air > /dev/null || go install github.com/cosmtrek/air@latest
	air

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GO) mod download
	$(GO) mod tidy

# Update dependencies
deps-update:
	@echo "Updating dependencies..."
	$(GO) get -u ./...
	$(GO) mod tidy

# Show help
help:
	@echo "Proxmox Guardian Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make              - Lint, test, and build"
	@echo "  make build        - Build binary"
	@echo "  make build-all    - Build for all platforms"
	@echo "  make test         - Run tests"
	@echo "  make lint         - Run linter"
	@echo "  make fmt          - Format code"
	@echo "  make clean        - Remove build artifacts"
	@echo "  make install      - Install binary locally"
	@echo "  make install-service - Install systemd service"
	@echo "  make uninstall    - Remove installation"
	@echo "  make dev          - Run validate locally"
	@echo "  make deps         - Download dependencies"
	@echo "  make help         - Show this help"
