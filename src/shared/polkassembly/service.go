package polkassembly

import (
	"fmt"
	"log"
	"strings"
	"sync"

	sharedgov "github.com/stake-plus/govcomms/src/shared/gov"
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

		prefix := uint16(42)
		if net.SS58Prefix != nil {
			prefix = *net.SS58Prefix
		}

		signer, err := NewPolkadotSignerFromSeed(seed, prefix)
		if err != nil {
			return nil, fmt.Errorf("create signer for network %s: %w", net.Name, err)
		}

		key := strings.ToLower(net.Name)
		svc.clients[key] = NewClient(cfg.Endpoint, signer)
		svc.logger.Printf("polkassembly: configured client for %s (address: %s)", key, signer.Address())
	}

	if len(svc.clients) == 0 {
		return nil, fmt.Errorf("polkassembly: no networks with seeds configured")
	}

	return svc, nil
}

// PostFirstMessage posts the first feedback message to Polkassembly and returns the comment ID.
func (s *Service) PostFirstMessage(network string, refID int, message, link string) (int, error) {
	key := strings.ToLower(network)

	s.mu.Lock()
	client, ok := s.clients[key]
	s.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("polkassembly: no client configured for network %s", network)
	}

	content := fmt.Sprintf("%s\n\n[Continue discussion with the DAO](%s)", message, link)

	if !client.IsLoggedIn() {
		s.logger.Printf("polkassembly: authenticating for %s", key)
		if err := client.Signup(key); err != nil {
			if err := client.Login(); err != nil {
				return 0, fmt.Errorf("polkassembly: authentication failed for %s: %w", network, err)
			}
		}
	}

	commentID, err := client.PostComment(content, refID, key)
	if err != nil {
		return 0, fmt.Errorf("polkassembly: post comment failed for %s ref %d: %w", network, refID, err)
	}

	if commentID != 0 {
		s.logger.Printf("polkassembly: posted comment %d for %s ref #%d", commentID, key, refID)
	} else {
		s.logger.Printf("polkassembly: posted comment for %s ref #%d (no id returned)", key, refID)
	}

	return commentID, nil
}
