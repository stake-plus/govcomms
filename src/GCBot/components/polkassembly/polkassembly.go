package polkassembly

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/stake-plus/govcomms/src/GCApi/data"
	"gorm.io/gorm"
)

// Service wraps the Polkassembly client for the bot
type Service struct {
	clients map[string]*Client // One client per network
	logger  *log.Logger
	db      *gorm.DB
}

// NewService creates a new Polkassembly service
func NewService(logger *log.Logger, db *gorm.DB) (*Service, error) {
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
		db:      db,
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

	// Load settings if database is available
	var intro, outro string
	if s.db != nil {
		// Load settings from database
		if err := data.LoadSettings(s.db); err != nil {
			s.logger.Printf("Failed to load settings: %v", err)
		}

		intro = data.GetSetting("polkassembly_intro")
		outro = data.GetSetting("polkassembly_outro")
	}

	// Use defaults if not set
	if intro == "" {
		intro = "## 🏛️ REEEEEEEEEE DAO Feedback\n\nThe **REEEEEEEEEE DAO** is a decentralized collective of governance participants dedicated to providing thoughtful feedback on Polkadot OpenGov proposals. Our members carefully review each referendum to ensure the best outcomes for the ecosystem.\n\n### 📋 Community Feedback"
	}
	if outro == "" {
		outro = "\n\n---\n\n### 💬 Continue the Discussion\n\nWe welcome proponents to engage directly with our DAO members for more detailed feedback and discussion. Our governance communication platform allows for secure, on-chain authenticated dialogue between proposers and the DAO.\n\n👉 **[Continue discussion with the DAO]({link})**\n\n*This feedback represents the collective voice of REEEEEEEEEE DAO members participating in Polkadot governance.*"
	}

	// Format the message with proper structure
	link := fmt.Sprintf("%s/%s/%d", gcURL, networkLower, refID)

	// Replace {link} placeholder in outro
	formattedOutro := strings.ReplaceAll(outro, "{link}", link)

	// Format the feedback with indentation (using blockquote style)
	indentedFeedback := "> " + strings.ReplaceAll(message, "\n", "\n> ")

	// Combine all parts
	content := fmt.Sprintf("%s\n\n%s\n%s", intro, indentedFeedback, formattedOutro)

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
