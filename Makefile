# File: Makefile

.PHONY: all build test clean docker api discordbot frontend indexer migrate

# Variables
DOCKER_COMPOSE = docker-compose
GO = go
NPM = npm

# Default target
all: build

# Build all components
build: api discordbot frontend

# Build API
api:
	@echo "Building API..."
	$(GO) build -o bin/api.exe ./src/api

# Build Discord bot
discordbot:
	@echo "Building Discord bot..."
	$(GO) build -o bin/discordbot.exe ./src/discordbot

# Build indexer service
indexer:
	@echo "Building indexer..."
	$(GO) build -o bin/indexer.exe ./src/indexer

# Build frontend
frontend:
	@echo "Building frontend..."
	cd src/frontend && $(NPM) install && $(NPM) run build
	@if not exist public mkdir public
	xcopy /E /Y /I src\frontend\dist public

# Run tests
test:
	@echo "Running tests..."
	$(GO) test ./...

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@if exist bin rmdir /S /Q bin
	@if exist public rmdir /S /Q public

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