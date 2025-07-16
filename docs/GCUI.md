# GCUI - GovComms User Interface

## Overview

GCUI is the React-based web frontend for the GovComms platform. It provides the interface for referendum proponents to authenticate, view feedback from DAO members, and respond to messages. The UI emphasizes security, simplicity, and a seamless user experience.

## Features

- **Multi-Method Authentication**: Support for WalletConnect, Polkadot.js, and air-gapped signing
- **Real-Time Messaging**: Live updates of conversation threads
- **Markdown Support**: Rich text formatting for messages
- **Responsive Design**: Works on desktop and mobile devices
- **Dark Theme**: Optimized for extended reading sessions
- **Security First**: All communications are authenticated and encrypted

## Architecture

### Technology Stack

- **React 18**: Core framework
- **TypeScript**: Type safety and better developer experience
- **React Router**: Client-side routing
- **TanStack Query**: Data fetching and caching
- **Vite**: Build tool and development server
- **Styled Components**: Component styling

### Project Structure

    src/
    ├── pages/
    │   ├── Home.tsx         # Landing page with referendum selection
    │   ├── Auth.tsx         # Authentication methods
    │   └── Proposal.tsx     # Message thread view
    ├── types/
    │   └── index.ts         # TypeScript type definitions
    ├── utils/
    │   ├── api.ts           # API client functions
    │   └── auth.ts          # Authentication utilities
    ├── App.tsx              # Main application component
    ├── main.tsx             # Application entry point
    ├── config.ts            # Configuration settings
    └── style.css            # Global styles

## User Flow

### 1. Landing Page

Users arrive at the home page and:
- Select network (Polkadot/Kusama)
- Enter referendum number
- Click Continue to authenticate

### 2. Authentication

Three authentication methods available:

**WalletConnect:**
- Click Connect Wallet
- Scan QR code with mobile wallet
- Approve connection
- Sign authentication message

**Polkadot.js Extension:**
- Click Connect Extension
- Select account from popup
- Sign authentication message

**Air-Gapped:**
- Enter Polkadot address
- Receive nonce to sign
- Submit system.remark on-chain
- Wait for automatic verification

### 3. Message Thread

Once authenticated, users can:
- View all messages for the referendum
- See referendum details (title, status, proposer)
- Send new messages with markdown formatting
- View message history with timestamps

## Configuration

### Environment Variables

Create `.env` file:

    VITE_API_URL=https://api.your-domain.com/v1
    VITE_GC_URL=https://your-domain.com
    VITE_WC_PROJECT_ID=your-walletconnect-project-id

### Build Configuration

Vite configuration (vite.config.ts):

    import { defineConfig } from 'vite';
    import react from '@vitejs/plugin-react';

    export default defineConfig({
      plugins: [react()],
      server: {
        port: 3000,
        proxy: {
          '/v1': {
            target: 'https://localhost:443',
            changeOrigin: true,
          }
        }
      }
    });

## Development

### Setup

Install dependencies:

    cd src/GCUI
    npm install

### Running Locally

Start development server:

    npm run dev

The app will be available at http://localhost:3000

### Building for Production

Create production build:

    npm run build

Output will be in `dist/` directory.

### Type Checking

Run TypeScript compiler:

    npm run type-check

### Linting

Check code style:

    npm run lint

## Components

### Home Page

Landing page with referendum selection:
- Network dropdown (Polkadot/Kusama)
- Referendum number input
- Error display for navigation errors
- Continue button to authentication

### Authentication Page

Supports three authentication methods:
- Method selection grid
- Dynamic content based on selection
- Loading states during authentication
- Error handling with user-friendly messages

### Proposal Page

Main messaging interface:
- Header with user info and logout
- Referendum details section
- Message history with timestamps
- Message input with markdown preview
- Real-time updates via polling

## API Integration

### Authentication Flow

    // 1. Request challenge
    POST /v1/auth/challenge
    Body: { address, method }
    Response: { nonce }

    // 2. Sign nonce (varies by method)
    
    // 3. Verify signature
    POST /v1/auth/verify
    Body: { address, method, signature, refId, network }
    Response: { token }

### Message Operations

    // Get messages
    GET /v1/messages/:network/:id
    Headers: { Authorization: Bearer <token> }
    
    // Send message
    POST /v1/messages
    Headers: { Authorization: Bearer <token> }
    Body: { proposalRef, body, emails }

## Styling

### Design System

The UI uses a dark theme with:
- Primary color: #e6007a (Polkadot pink)
- Secondary color: #6d28d9 (Purple)
- Background: #0a0a0a (Near black)
- Text: #ffffff (White)

### Responsive Breakpoints

- Mobile: < 768px
- Tablet: 768px - 1024px
- Desktop: > 1024px

### Component Styling

Uses CSS custom properties for theming:

    :root {
      --primary-color: #e6007a;
      --secondary-color: #6d28d9;
      --bg-dark: #0a0a0a;
      --bg-light: #1a1a1a;
      --text-primary: #ffffff;
      --text-secondary: #a0a0a0;
    }

## Security

### JWT Storage

- Tokens stored in sessionStorage
- Cleared on tab close
- Never stored in localStorage

### XSS Prevention

- All user input sanitized
- Markdown rendered safely
- Content Security Policy headers

### CORS Configuration

- Restricted to allowed origins
- Credentials included for auth
- Proper CORS headers required

## Error Handling

### Network Errors

- Automatic retry with exponential backoff
- User-friendly error messages
- Fallback UI for failed requests

### Authentication Errors

- Clear error messages for each failure type
- Redirect to home on authorization failure
- Session expiry handling

### Validation

- Client-side form validation
- Referendum number format checking
- Message length limits (10-5000 chars)

## Performance

### Optimization Techniques

1. **Code Splitting**
   - Route-based splitting
   - Lazy loading of components

2. **Caching**
   - TanStack Query caching
   - 5-second refetch interval

3. **Bundle Size**
   - Tree shaking enabled
   - Minimal dependencies

### Monitoring

Key metrics to track:
- Time to Interactive (TTI)
- First Contentful Paint (FCP)
- API response times
- Bundle size

## Deployment

### Static Hosting

The built app can be served from any static host:

1. Build the application:

       npm run build

2. Upload `dist/` contents to web server

3. Configure server for SPA routing

### Docker Deployment

Dockerfile example:

    FROM node:18-alpine as builder
    WORKDIR /app
    COPY package*.json ./
    RUN npm ci
    COPY . .
    RUN npm run build

    FROM nginx:alpine
    COPY --from=builder /app/dist /usr/share/nginx/html
    COPY nginx.conf /etc/nginx/conf.d/default.conf
    EXPOSE 80

## Troubleshooting

### Common Issues

1. **Blank Page**
   - Check browser console for errors
   - Verify API URL configuration
   - Ensure proper routing setup

2. **Authentication Fails**
   - Verify wallet extension installed
   - Check network connectivity
   - Ensure correct referendum access

3. **Messages Not Loading**
   - Check authentication token
   - Verify API endpoint
   - Review network tab for errors

4. **Styling Issues**
   - Clear browser cache
   - Check CSS file loaded
   - Verify build process

### Debug Mode

Enable debug logging:

    localStorage.setItem('debug', 'govcomms:*');

## Browser Support

Minimum supported versions:
- Chrome 90+
- Firefox 88+
- Safari 14+
- Edge 90+

## Accessibility

- Semantic HTML structure
- ARIA labels where needed
- Keyboard navigation support
- Screen reader compatible
- High contrast mode support