package polkassembly

import (
	"fmt"
	"log"
	"strings"
	"sync"

	sharedgov "github.com/stake-plus/govcomms/src/polkadot-go/governance"
)

// ServiceConfig describes configuration for the Polkassembly service.
type ServiceConfig struct {
	Endpoint string
	Logger   *log.Logger
}

// Service manages Polkassembly clients for multiple networks.
type Service struct {
	mu      sync.Mutex
	clients map[string]*Client
	logger  *log.Logger
	cfg     ServiceConfig
}

// NewService creates a new service configured using network data from the database.
func NewService(cfg ServiceConfig, networks map[uint8]*sharedgov.Network) (*Service, error) {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://api.polkassembly.io/api/v1"
	}

	svc := &Service{
		clients: make(map[string]*Client),
		logger:  cfg.Logger,
		cfg:     cfg,
	}

	for _, net := range networks {
		seed := strings.TrimSpace(net.PolkassemblySeed)
		if seed == "" {
			continue
		}

		// Polkassembly verifies accounts against generic Substrate (SS58 prefix 42, addresses starting with 5),
		// so force that prefix even if the chain uses a different network-specific prefix.
		prefix := uint16(42)

		signer, err := NewPolkadotSignerFromSeed(seed, prefix)
		if err != nil {
			svc.logger.Printf("polkassembly: unable to configure signer for %s: %v", net.Name, err)
			continue
		}

		key := strings.ToLower(net.Name)
		svc.clients[key] = NewClient(cfg.Endpoint, signer, key)
		svc.logger.Printf("polkassembly: configured client for %s (address: %s)", key, signer.Address())
	}

	if len(svc.clients) == 0 {
		return nil, fmt.Errorf("polkassembly: no networks with seeds configured")
	}

	return svc, nil
}

// ListComments retrieves all comments for a referendum post.
func (s *Service) ListComments(network string, postID int) ([]Comment, error) {
	key := strings.ToLower(network)

	s.mu.Lock()
	client, ok := s.clients[key]
	s.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("polkassembly: no client configured for network %s", network)
	}

	return client.ListComments(postID, key)
}

// PostFirstMessage posts the first feedback message to Polkassembly and returns the comment ID.
func (s *Service) PostFirstMessage(network string, refID int, message, link string) (string, error) {
	key := strings.ToLower(network)

	s.mu.Lock()
	client, ok := s.clients[key]
	s.mu.Unlock()
	if !ok {
		s.logger.Printf("polkassembly: no client configured for network %s", network)
		return "", fmt.Errorf("polkassembly: no client configured for network %s", network)
	}

	content := fmt.Sprintf("%s\n\n[Continue discussion with the DAO](%s)", message, link)

	s.logger.Printf("polkassembly: PostFirstMessage called for %s ref #%d", key, refID)
	if !client.IsLoggedIn() {
		s.logger.Printf("polkassembly: authenticating for %s", key)
		if err := client.Signup(key); err != nil {
			s.logger.Printf("polkassembly: signup failed, trying login: %v", err)
			if err := client.Login(); err != nil {
				s.logger.Printf("polkassembly: login also failed: %v", err)
				return "", fmt.Errorf("polkassembly: authentication failed for %s: %w", network, err)
			}
		}
		s.logger.Printf("polkassembly: authentication successful for %s", key)
	} else {
		s.logger.Printf("polkassembly: already logged in for %s", key)
	}

	s.logger.Printf("polkassembly: posting comment for %s ref #%d", key, refID)
	commentID, err := client.PostComment(content, refID, key)
	if err != nil {
		s.logger.Printf("polkassembly: PostComment returned error: %v", err)
		return "", fmt.Errorf("polkassembly: post comment failed for %s ref %d: %w", network, refID, err)
	}

	if commentID == "" {
		s.logger.Printf("polkassembly: PostComment returned empty comment ID for %s ref #%d", key, refID)
		return "", fmt.Errorf("polkassembly: post comment succeeded but no comment ID returned for %s ref #%d", key, refID)
	}

	s.logger.Printf("polkassembly: posted comment %s for %s ref #%d", commentID, key, refID)
	return commentID, nil
}
