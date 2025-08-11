# Variables
BINARY_NAME=govcomms-bot
BUILD_DIR=bin
SRC_DIR=src/bot
POLKADOT_DIR=src/polkadot-go

# Go commands
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt

# Build variables
BINARY_UNIX=$(BUILD_DIR)/$(BINARY_NAME)
BINARY_WIN=$(BUILD_DIR)/$(BINARY_NAME).exe

# Get OS
ifeq ($(OS),Windows_NT)
    BINARY=$(BINARY_WIN)
    RM=del /Q
    MKDIR=mkdir
else
    BINARY=$(BINARY_UNIX)
    RM=rm -f
    MKDIR=mkdir -p
endif

# Phony targets
.PHONY: all build clean test deps run fmt help

# Default target
all: deps build

# Help target
help:
	@echo Available targets:
	@echo   make build    - Build the bot binary
	@echo   make run      - Run the bot directly
	@echo   make clean    - Clean build artifacts
	@echo   make deps     - Download dependencies
	@echo   make test     - Run tests
	@echo   make fmt      - Format code
	@echo   make all      - Download deps and build

# Download dependencies
deps:
	$(GOMOD) download
	$(GOMOD) tidy

# Build the bot binary
build:
	@echo Building bot...
	@$(MKDIR) $(BUILD_DIR)
	$(GOBUILD) -o $(BINARY) $(SRC_DIR)/main.go
	@echo Bot built: $(BINARY)

# Build for specific platforms
build-windows:
	@echo Building for Windows...
	@$(MKDIR) $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 $(GOBUILD) -o $(BINARY_WIN) $(SRC_DIR)/main.go
	@echo Windows binary built: $(BINARY_WIN)

build-linux:
	@echo Building for Linux...
	@$(MKDIR) $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BINARY_UNIX) $(SRC_DIR)/main.go
	@echo Linux binary built: $(BINARY_UNIX)

build-mac:
	@echo Building for macOS...
	@$(MKDIR) $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 $(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin $(SRC_DIR)/main.go
	@echo macOS binary built: $(BUILD_DIR)/$(BINARY_NAME)-darwin

# Build all platforms
build-all: build-windows build-linux build-mac

# Run the bot directly
run:
	@echo Running bot...
	$(GOCMD) run $(SRC_DIR)/main.go

# Clean build artifacts
clean:
	@echo Cleaning...
	$(GOCLEAN)
	@$(RM) $(BUILD_DIR)$(if $(findstring Windows_NT,$(OS)),\*,/*)
	@echo Clean complete

# Run tests
test:
	@echo Running tests...
	$(GOTEST) -v ./$(SRC_DIR)/...
	$(GOTEST) -v ./$(POLKADOT_DIR)/...

# Format code
fmt:
	@echo Formatting code...
	$(GOFMT) ./$(SRC_DIR)/...
	$(GOFMT) ./$(POLKADOT_DIR)/...
	@echo Formatting complete

# Development mode with auto-restart on file changes (requires entr or watchexec)
dev:
	@echo Starting in development mode...
	$(GOCMD) run $(SRC_DIR)/main.go