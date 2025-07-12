# File: Makefile

.PHONY: all build test clean docker api discordbot frontend indexer migrate

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
build: api discordbot frontend

# Build API
api:
	@echo "Building API..."
	$(GO) build -o bin/api$(EXE) ./src/api

# Build Discord bot
discordbot:
	@echo "Building Discord bot..."
	$(GO) build -o bin/discordbot$(EXE) ./src/discordbot

# Build indexer service
indexer:
	@echo "Building indexer..."
	$(GO) build -o bin/indexer$(EXE) ./src/indexer

# Build frontend
frontend:
	@echo "Building frontend..."
	cd src/frontend && $(NPM) install && $(NPM) run build
	$(MKDIR) public
	$(CP) src$(SEP)frontend$(SEP)dist$(SEP)* public$(SEP)

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
	$(GO) run ./src/api

dev-bot:
	$(GO) run ./src/discordbot

dev-frontend:
	cd src/frontend && $(NPM) run dev

# Database migration
migrate:
	@echo "Running database migrations..."
	$(GO) run ./scripts/migrate

# Install dependencies
deps:
	@echo "Installing Go dependencies..."
	$(GO) mod download
	@echo "Installing frontend dependencies..."
	cd src/frontend && $(NPM) install