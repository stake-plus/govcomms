# Variables
API_BINARY = bin/api
DISCORD_BINARY = bin/discordbot
FRONTEND_DIR = public
SRC_API = src/api
SRC_DISCORD = src/discordbot
SRC_FRONTEND = src/frontend

# Default target
all: clean api discord frontend

# Build API
api:
	@echo "Building API..."
	@mkdir -p bin
	cd $(SRC_API) && go build -o ../../$(API_BINARY) .
	@echo "API built: $(API_BINARY)"

# Build Discord bot
discord:
	@echo "Building Discord bot..."
	@mkdir -p bin
	cd $(SRC_DISCORD) && go build -o ../../$(DISCORD_BINARY) .
	@echo "Discord bot built: $(DISCORD_BINARY)"

# Build frontend
frontend:
	@echo "Building frontend..."
	@mkdir -p $(FRONTEND_DIR)
	cd $(SRC_FRONTEND) && npm install && npm run build
	@cp -r $(SRC_FRONTEND)/dist/* $(FRONTEND_DIR)/
	@echo "Frontend built: $(FRONTEND_DIR)"

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf bin $(FRONTEND_DIR)
	@echo "Clean complete"

# Development targets
dev-api:
	cd $(SRC_API) && go run .

dev-discord:
	cd $(SRC_DISCORD) && go run .

dev-frontend:
	cd $(SRC_FRONTEND) && npm run dev

# Install dependencies
deps:
	@echo "Installing Go dependencies..."
	cd $(SRC_API) && go mod download
	cd $(SRC_DISCORD) && go mod download
	@echo "Installing frontend dependencies..."
	cd $(SRC_FRONTEND) && npm install

# Run tests
test:
	cd $(SRC_API) && go test ./...
	cd $(SRC_DISCORD) && go test ./...
	cd $(SRC_FRONTEND) && npm test

# Create systemd service files
install-services:
	@echo "Creating systemd service files..."
	@sudo cp scripts/govcomms-api.service /etc/systemd/system/
	@sudo cp scripts/govcomms-discord.service /etc/systemd/system/
	@sudo systemctl daemon-reload
	@echo "Services installed. Run 'make start-services' to start them."

# Start services
start-services:
	@sudo systemctl start govcomms-api
	@sudo systemctl start govcomms-discord
	@sudo systemctl enable govcomms-api
	@sudo systemctl enable govcomms-discord

# Stop services
stop-services:
	@sudo systemctl stop govcomms-api
	@sudo systemctl stop govcomms-discord

# Check service status
status:
	@sudo systemctl status govcomms-api
	@sudo systemctl status govcomms-discord

.PHONY: all api discord frontend clean dev-api dev-discord dev-frontend deps test install-services start-services stop-services status