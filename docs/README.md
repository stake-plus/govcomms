# GovComms - Polkadot Governance Communication System

GovComms is a system that facilitates communication between Polkadot/Kusama referendum proposers and DAO members. It integrates with Discord for DAO voting and provides a web interface for secure messaging.

## Features

- Secure authentication via Polkadot.js, WalletConnect, or air-gapped signing
- Discord bot integration for DAO feedback
- First message auto-posts to Polkassembly
- Authorization system ensuring only relevant participants can communicate
- Multi-network support (Polkadot & Kusama)
- Real-time referendum indexing

## Prerequisites

- Go 1.21+
- Node.js 18+
- MySQL 8.0+
- Redis 6.0+
- Discord Bot Token

## Building

Install dependencies:
    make deps

Build all components:
    make

Or build individually:
    make api
    make discord
    make frontend

## Configuration

1. Copy the example environment file:
    cp .env.example .env

2. Configure database settings in MySQL:
    INSERT INTO settings (name, value) VALUES 
    ('gc_url', 'https://your-frontend-url.com'),
    ('gcapi_url', 'https://your-api-url.com'),
    ('polkassembly_api', 'https://api.polkassembly.io/api/v1'),
    ('walletconnect_project_id', 'your_project_id'),
    ('jwt_secret', 'your_secure_jwt_secret');

## Deployment

1. Build the project:
    make

2. Run the setup script:
    sudo ./scripts/setup.sh

3. Configure environment:
    sudo nano /opt/govcomms/.env

4. Install and start services:
    make install-services
    make start-services

5. Set up nginx (optional):
    sudo cp scripts/nginx.conf /etc/nginx/sites-available/govcomms
    sudo ln -s /etc/nginx/sites-available/govcomms /etc/nginx/sites-enabled/
    sudo nginx -t && sudo systemctl reload nginx

## Development

Run API in development:
    make dev-api

Run Discord bot in development:
    make dev-discord

Run frontend in development:
    make dev-frontend

## Architecture

- API Server (src/api/): Handles authentication, message storage, and integration with Polkassembly
- Discord Bot (src/discordbot/): Processes feedback commands and posts messages to referendum threads
- Frontend (src/frontend/): React-based web interface for authenticated messaging
- Polkadot Client (src/polkadot-go/): Blockchain indexer for referendum data

## Database Schema

The system uses MySQL with the following main tables:
- networks: Blockchain networks (Polkadot, Kusama)
- refs: Referendum proposals
- ref_messages: Messages between proposers and DAO
- ref_proponents: Authorized participants
- dao_members: DAO member registry
- settings: System configuration

## API Endpoints

- POST /v1/auth/challenge: Request authentication challenge
- POST /v1/auth/verify: Verify signature and get JWT
- GET /v1/messages/:network/:id: Get messages for a referendum
- POST /v1/messages: Send a new message
- GET /v1/votes/:network/:id: Get vote summary
- POST /v1/votes: Cast a vote

## Discord Commands

- !feedback network/ref_number Your feedback message: Submit feedback for a referendum

## Security

- JWT-based authentication
- Rate limiting on all endpoints
- Authorization checks for referendum participation
- HTML escaping for user input
- CORS configuration for allowed origins

## Monitoring

Check service status:
    make status

View logs:
    tail -f /var/log/govcomms/api.log
    tail -f /var/log/govcomms/discord.log

