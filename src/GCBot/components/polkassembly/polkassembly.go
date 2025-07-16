package polkassembly

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Service wraps the Polkassembly client for the bot
type Service struct {
	clients map[string]*Client // One client per network
	logger  *log.Logger
}

// NewService creates a new Polkassembly service
func NewService(logger *log.Logger) (*Service, error) {
	// Get configuration from environment
	endpoint := os.Getenv("POLKASSEMBLY_ENDPOINT")
	if endpoint == "" {
		endpoint = "https://api.polkassembly.io/api/v2"
	}

	clients := make(map[string]*Client)

	// Setup Polkadot client
	polkadotSeed := os.Getenv("POLKASSEMBLY_POLKADOT_SEED")
	if polkadotSeed != "" {
		logger.Printf("Creating Polkadot signer from seed...")
		logger.Printf("Seed phrase has %d words", len(strings.Fields(polkadotSeed)))

		signer, err := NewPolkadotSignerFromSeed(polkadotSeed, 0)
		if err != nil {
			return nil, fmt.Errorf("create polkadot signer: %w", err)
		}
		clients["polkadot"] = NewClient(endpoint, signer)
		logger.Printf("Polkassembly client configured for Polkadot with address: %s", signer.Address())
	}

	// Setup Kusama client
	kusamaSeed := os.Getenv("POLKASSEMBLY_KUSAMA_SEED")
	if kusamaSeed != "" {
		logger.Printf("Creating Kusama signer from seed...")
		logger.Printf("Seed phrase has %d words", len(strings.Fields(kusamaSeed)))

		signer, err := NewPolkadotSignerFromSeed(kusamaSeed, 2)
		if err != nil {
			return nil, fmt.Errorf("create kusama signer: %w", err)
		}
		clients["kusama"] = NewClient(endpoint, signer)
		logger.Printf("Polkassembly client configured for Kusama with address: %s", signer.Address())
	}

	// If no network-specific seeds, try generic seed for both networks
	if len(clients) == 0 {
		genericSeed := os.Getenv("POLKASSEMBLY_SEED")
		if genericSeed == "" {
			return nil, fmt.Errorf("no POLKASSEMBLY_SEED environment variables set")
		}

		logger.Printf("Using generic seed for both networks...")
		logger.Printf("Seed phrase has %d words", len(strings.Fields(genericSeed)))

		// Create signers for both networks with the same seed
		polkadotSigner, err := NewPolkadotSignerFromSeed(genericSeed, 0)
		if err != nil {
			return nil, fmt.Errorf("create polkadot signer: %w", err)
		}
		clients["polkadot"] = NewClient(endpoint, polkadotSigner)

		kusamaSigner, err := NewPolkadotSignerFromSeed(genericSeed, 2)
		if err != nil {
			return nil, fmt.Errorf("create kusama signer: %w", err)
		}
		clients["kusama"] = NewClient(endpoint, kusamaSigner)

		logger.Printf("Polkassembly clients configured with generic seed")
		logger.Printf("Polkadot address: %s", polkadotSigner.Address())
		logger.Printf("Kusama address: %s", kusamaSigner.Address())
	}

	return &Service{
		clients: clients,
		logger:  logger,
	}, nil
}

// PostFirstMessage posts the first feedback message to Polkassembly
func (s *Service) PostFirstMessage(network string, refID int, message string, gcURL string) error {
	s.logger.Printf("PostFirstMessage called for %s referendum #%d", network, refID)

	networkLower := strings.ToLower(network)
	client, exists := s.clients[networkLower]
	if !exists {
		return fmt.Errorf("no Polkassembly client configured for network %s", network)
	}

	// Format the message with link
	link := fmt.Sprintf("%s/%s/%d", gcURL, networkLower, refID)
	content := fmt.Sprintf("%s\n\n[Continue discussion with the DAO](%s)", message, link)

	s.logger.Printf("Attempting to post comment to Polkassembly for %s #%d", network, refID)
	s.logger.Printf("Content length: %d characters", len(content))

	// Create a context with a longer timeout for the entire operation
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Post the comment with context
	if err := client.PostCommentWithContext(ctx, content, refID, networkLower); err != nil {
		s.logger.Printf("Error posting to Polkassembly: %v", err)
		return fmt.Errorf("post comment: %w", err)
	}

	s.logger.Printf("Successfully posted first message to Polkassembly for %s referendum #%d", network, refID)
	return nil
}

// ParseReferendumRef parses a referendum reference like "polkadot/123"
func ParseReferendumRef(ref string) (network string, id int, err error) {
	parts := strings.Split(ref, "/")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid referendum reference format")
	}

	network = parts[0]
	id, err = strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, fmt.Errorf("invalid referendum ID: %w", err)
	}

	return network, id, nil
}

// TestConnection tests the connection and authentication with Polkassembly
func (s *Service) TestConnection(network string) error {
	networkLower := strings.ToLower(network)
	client, exists := s.clients[networkLower]
	if !exists {
		return fmt.Errorf("no Polkassembly client configured for network %s", network)
	}

	// Test authentication
	s.logger.Printf("Testing Polkassembly connection for %s", network)

	// Login to the specific network
	if err := client.Login(networkLower); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	// Test fetching user ID
	userID, err := client.fetchUserID(networkLower)
	if err != nil {
		return fmt.Errorf("fetch user ID failed: %w", err)
	}

	s.logger.Printf("Successfully authenticated to Polkassembly as user ID: %d", userID)
	return nil
}
