# File: Makefile

.PHONY: all build test clean docker GCApi GCBot GCUI indexer migrate

# Variables
DOCKER_COMPOSE = docker-compose
GO = go
NPM = npm

# OS detection
ifeq ($(OS),Windows_NT)
    RM = cmd /C if exist
    RMDIR = rmdir /S /Q
    MKDIR = mkdir
    CP = xcopy /E /Y /I
    EXE = .exe
    SEP = \\
else
    RM = rm -f
    RMDIR = rm -rf
    MKDIR = mkdir -p
    CP = cp -r
    EXE =
    SEP = /
endif

# Default target
all: build

# Build all components
build: GCApi GCBot GCUI

# Build API
GCApi:
	@echo "Building API..."
	$(GO) build -o bin/GCApi$(EXE) ./src/GCApi

# Build Discord bot
GCBot:
	@echo "Building Discord bot..."
	$(GO) build -o bin/GCBot$(EXE) ./src/GCBot

# Build gcui
GCUI:
	@echo "Building GCUI..."
	cd src/GCUI && $(NPM) install && $(NPM) run build
	$(MKDIR) public
	$(CP) src$(SEP)GCUI$(SEP)dist$(SEP)* public$(SEP)

# Run tests
test:
	@echo "Running tests..."
	$(GO) test ./...

# Clean build artifacts
clean:
	@echo "Cleaning..."
	$(RMDIR) bin
	$(RMDIR) public

# Docker commands
docker-up:
	$(DOCKER_COMPOSE) up -d

docker-down:
	$(DOCKER_COMPOSE) down

docker-build:
	$(DOCKER_COMPOSE) build

# Development commands
dev-api:
	$(GO) run ./src/GCApi

dev-bot:
	$(GO) run ./src/GCBot

dev-frontend:
	cd src/frontend && $(NPM) run dev

# Install dependencies
deps:
	@echo "Installing Go dependencies..."
	$(GO) mod download
	@echo "Installing frontend dependencies..."
	cd src/GCUI && $(NPM) install