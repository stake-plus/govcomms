# GCApi - GovComms API Server

## Overview

GCApi is the core backend service of the GovComms platform. It provides RESTful endpoints for authentication, message management, and voting operations. The API handles all business logic, database operations, and integrations with external services.

## Architecture

### Components

1. **Web Server**: Gin-based HTTP server with SSL/TLS support
2. **Authentication**: JWT-based auth with multiple wallet connection methods
3. **Database**: MySQL with GORM ORM for data persistence
4. **Cache**: Redis for session management and message queuing
5. **Indexer**: Multi-network blockchain indexer for referendum data
6. **Integrations**: Polkassembly API for posting feedback

### Key Features

- Multi-signature authentication (WalletConnect, Polkadot.js, Air-gap)
- Rate limiting and security middleware
- Real-time referendum data indexing
- Message threading and authorization
- DAO voting aggregation

## API Endpoints

### Authentication

#### POST /v1/auth/challenge
Request a nonce for authentication.

**Request:**
    {
      "address": "5GrwvaEF5zXb26Fz9rcQpDWS...",
      "method": "polkadotjs" // or "walletconnect" or "airgap"
    }

**Response:**
    {
      "nonce": "0x1234567890abcdef..."
    }

#### POST /v1/auth/verify
Verify signature and get JWT token.

**Request:**
    {
      "address": "5GrwvaEF5zXb26Fz9rcQpDWS...",
      "method": "polkadotjs",
      "signature": "0xabcdef...",
      "refId": "123",
      "network": "polkadot"
    }

**Response:**
    {
      "token": "eyJhbGciOiJIUzI1NiIs..."
    }

### Messages (Requires Authentication)

#### POST /v1/messages
Create a new message for a referendum.

**Headers:**
    Authorization: Bearer <token>

**Request:**
    {
      "proposalRef": "polkadot/123",
      "body": "Message content with **markdown** support",
      "emails": ["optional@email.com"]
    }

**Response:**
    {
      "id": 456
    }

#### GET /v1/messages/:network/:id
Get all messages for a referendum.

**Response:**
    {
      "proposal": {
        "id": 1,
        "network": "polkadot",
        "ref_id": 123,
        "title": "Treasury Proposal",
        "submitter": "5GrwvaEF...",
        "status": "Ongoing",
        "track_id": 30
      },
      "messages": [
        {
          "ID": 456,
          "Author": "5GrwvaEF...",
          "Body": "Message content",
          "CreatedAt": "2024-01-15T10:30:00Z",
          "Internal": false
        }
      ]
    }

### Voting (DAO Members Only)

#### POST /v1/votes
Cast a vote on a referendum.

**Request:**
    {
      "proposalRef": "polkadot/123",
      "choice": "aye" // or "nay" or "abstain"
    }

#### GET /v1/votes/:network/:id
Get vote summary for a referendum.

**Response:**
    {
      "aye": 15,
      "nay": 3,
      "abstain": 2
    }

### Admin Endpoints

#### POST /v1/admin/discord/channel
Set Discord channel for a network (Admin only).

**Request:**
    {
      "networkId": 1,
      "discordChannelId": "1234567890"
    }

## Configuration

### Environment Variables

    # Database
    MYSQL_DSN=user:pass@tcp(host:port)/database

    # Cache
    REDIS_URL=redis://host:port/db

    # Security
    JWT_SECRET=your-secret-key

    # Server
    PORT=443
    SSL_CERT=/path/to/cert.pem
    SSL_KEY=/path/to/key.pem

    # Indexer
    POLL_INTERVAL=60

    # External APIs
    POLKASSEMBLY_API_KEY=your-api-key

### Database Schema

The API uses the following main tables:
- `networks`: Supported blockchain networks
- `refs`: Referendum data
- `ref_messages`: Messages between DAO and proponents
- `ref_proponents`: Authorized participants
- `dao_members`: DAO member list
- `dao_votes`: Internal voting records

## Security

### Authentication Flow

1. Client requests nonce with wallet address
2. Client signs nonce with private key
3. Client sends signature for verification
4. API validates signature and issues JWT
5. JWT required for all protected endpoints

### Rate Limiting

- Authentication: 5 attempts per minute
- API calls: 30 requests per minute
- Message creation: 5 messages per 5 minutes

### Authorization

- Messages: Only referendum participants can send/view
- Voting: Only DAO members can vote
- Admin: Only users with is_admin flag

## Indexer Service

The indexer continuously monitors blockchain data:

1. Polls configured RPC endpoints
2. Fetches referendum information
3. Decodes preimages to extract recipients
4. Updates database with latest state
5. Handles multiple networks concurrently

### Indexing Process

    // Runs every POLL_INTERVAL seconds
    1. Get current block number
    2. Fetch all referendum keys
    3. For each referendum:
       - Decode current state
       - Fetch historical data if needed
       - Extract participant addresses
       - Update database

## Error Handling

### HTTP Status Codes

- 200: Success
- 201: Created
- 400: Bad Request
- 401: Unauthorized
- 403: Forbidden
- 404: Not Found
- 429: Too Many Requests
- 500: Internal Server Error

### Error Response Format

    {
      "err": "Error message",
      "message": "Optional detailed message"
    }

## Deployment

### Building

    cd src/GCApi
    go build -o gcapi

### Running

    ./gcapi

### Health Check

The API logs startup information:

    Starting HTTPS server on port 443
    Successfully connected to Polkadot RPC
    Successfully connected to Kusama RPC
    GovComms API listening on 443 (SSL: true)

## Monitoring

### Logs

- Request logs: All HTTP requests with status codes
- Error logs: Failed operations with stack traces
- Indexer logs: Blockchain synchronization status

### Metrics

Monitor these key metrics:
- Response times
- Error rates
- Database connection pool
- Redis connection status
- RPC endpoint availability

## Troubleshooting

### Common Issues

1. **SSL Certificate Errors**
   - Verify cert/key file paths
   - Check certificate validity
   - Ensure proper file permissions

2. **Database Connection Failed**
   - Verify MySQL is running
   - Check credentials in DSN
   - Ensure database exists

3. **Indexer Not Syncing**
   - Check RPC endpoint connectivity
   - Verify network configuration
   - Review indexer logs

4. **Authentication Failures**
   - Ensure Redis is running
   - Check signature verification
   - Verify JWT secret matches

## API Client Example

    // Authentication
    const response = await fetch('https://api.govcomms.io/v1/auth/challenge', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        address: '5GrwvaEF...',
        method: 'polkadotjs'
      })
    });
    const { nonce } = await response.json();

    // Sign nonce with wallet...
    const signature = await wallet.sign(nonce);

    // Verify and get token
    const verifyResponse = await fetch('https://api.govcomms.io/v1/auth/verify', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        address: '5GrwvaEF...',
        method: 'polkadotjs',
        signature: signature
      })
    });
    const { token } = await verifyResponse.json();

    // Use token for authenticated requests
    const messages = await fetch('https://api.govcomms.io/v1/messages/polkadot/123', {
      headers: { 'Authorization': `Bearer ${token}` }
    });