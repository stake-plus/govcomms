package polkassembly

import (
	"log"

	sharedgov "github.com/stake-plus/govcomms/src/polkadot-go/governance"
)

// ServiceConfig describes configuration for the Polkassembly service.
type ServiceConfig struct {
	Endpoint string
	Logger   *log.Logger
}

// Service manages Polkassembly clients for multiple networks.
// Now uses the reference API implementation via ServiceWrapper
type Service struct {
	wrapper *ServiceWrapper
	logger  *log.Logger
	cfg     ServiceConfig
}

// NewService creates a new service using the reference API implementation
func NewService(cfg ServiceConfig, networks map[uint8]*sharedgov.Network) (*Service, error) {
	// Use the wrapper that implements the reference API
	wrapper, err := NewServiceWrapper(cfg, networks)
	if err != nil {
		return nil, err
	}

	return &Service{
		wrapper: wrapper,
		logger:  wrapper.logger,
		cfg:     cfg,
	}, nil
}

// ListComments retrieves all comments for a referendum post.
func (s *Service) ListComments(network string, postID int) ([]Comment, error) {
	return s.wrapper.ListComments(network, postID)
}

// PostFirstMessage posts the first feedback message to Polkassembly and returns the comment ID.
func (s *Service) PostFirstMessage(network string, refID int, message, link string) (string, error) {
	return s.wrapper.PostFirstMessage(network, refID, message, link)
}
