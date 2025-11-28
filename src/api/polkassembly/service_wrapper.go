package polkassembly

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	polkassemblyapi "github.com/polkadot-go/polkassembly-api"
	sharedgov "github.com/stake-plus/govcomms/src/polkadot-go/governance"
)

// Comment represents a comment returned by the Polkassembly API.
// This is our internal type that we convert from the reference API's Comment type.
type Comment struct {
	ID        int    `json:"id"`
	ParentID  *int   `json:"parent_id"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
	User      struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
	} `json:"user"`
}

// ParsedCreatedAt converts the comment timestamp into time.Time when possible.
func (c Comment) ParsedCreatedAt() time.Time {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, c.CreatedAt); err == nil {
			return ts
		}
	}
	return time.Time{}
}

// ServiceWrapper wraps the reference polkassembly-api library
type ServiceWrapper struct {
	mu      sync.Mutex
	clients map[string]*polkassemblyapi.Client
	logger  *log.Logger
}

// NewServiceWrapper creates a new service using the reference API implementation
func NewServiceWrapper(cfg ServiceConfig, networks map[uint8]*sharedgov.Network) (*ServiceWrapper, error) {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}

	wrapper := &ServiceWrapper{
		clients: make(map[string]*polkassemblyapi.Client),
		logger:  cfg.Logger,
	}

	for _, net := range networks {
		seed := strings.TrimSpace(net.PolkassemblySeed)
		if seed == "" {
			continue
		}

		key := strings.ToLower(net.Name)
		client := polkassemblyapi.NewClient(polkassemblyapi.Config{
			Network: key,
		})

		// Authenticate with seed
		if err := client.AuthenticateWithSeed(key, seed); err != nil {
			wrapper.logger.Printf("polkassembly: unable to authenticate for %s: %v", key, err)
			continue
		}

		wrapper.clients[key] = client
		wrapper.logger.Printf("polkassembly: configured client for %s", key)
	}

	if len(wrapper.clients) == 0 {
		return nil, fmt.Errorf("polkassembly: no networks with seeds configured")
	}

	return wrapper, nil
}

// ListComments retrieves all comments for a referendum post.
func (s *ServiceWrapper) ListComments(network string, postID int) ([]Comment, error) {
	key := strings.ToLower(network)

	s.mu.Lock()
	client, ok := s.clients[key]
	s.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("polkassembly: no client configured for network %s", network)
	}

	// Use reference API to get comments
	comments, err := client.GetPostCommentsByType(postID, "ReferendumV2")
	if err != nil {
		return nil, fmt.Errorf("get comments: %w", err)
	}

	// Convert to our Comment type, flattening replies
	result := make([]Comment, 0)
	var flattenComments func([]polkassemblyapi.Comment, *int)
	flattenComments = func(comments []polkassemblyapi.Comment, parentID *int) {
		for _, c := range comments {
			comment := Comment{}
			// Parse ID from string to int
			if id, err := strconv.Atoi(c.ID); err == nil {
				comment.ID = id
			}
			// Set parent ID if this is a reply
			if parentID != nil {
				comment.ParentID = parentID
			}
			// Convert Content from interface{} to string
			if contentStr, ok := c.Content.(string); ok {
				comment.Content = contentStr
			} else if contentStr := fmt.Sprintf("%v", c.Content); contentStr != "" {
				comment.Content = contentStr
			}
			// Convert CreatedAt from time.Time to string
			comment.CreatedAt = c.CreatedAt.Format(time.RFC3339)
			// Set user info
			comment.User.ID = c.UserID
			comment.User.Username = c.Username
			result = append(result, comment)
			// Recursively flatten replies
			if len(c.Replies) > 0 {
				parentIDVal := comment.ID
				flattenComments(c.Replies, &parentIDVal)
			}
		}
	}
	flattenComments(comments, nil)

	return result, nil
}

// PostFirstMessage posts the first feedback message to Polkassembly and returns the comment ID.
func (s *ServiceWrapper) PostFirstMessage(network string, refID int, message, link string) (string, error) {
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

	// Use reference API to add comment
	comment, err := client.AddComment("ReferendumV2", refID, polkassemblyapi.AddCommentRequest{
		Content: content,
	})
	if err != nil {
		s.logger.Printf("polkassembly: AddComment returned error: %v", err)
		return "", fmt.Errorf("polkassembly: post comment failed for %s ref %d: %w", network, refID, err)
	}

	if comment == nil || comment.ID == "" {
		s.logger.Printf("polkassembly: AddComment returned empty comment ID for %s ref #%d", key, refID)
		return "", fmt.Errorf("polkassembly: post comment succeeded but no comment ID returned for %s ref #%d", key, refID)
	}

	s.logger.Printf("polkassembly: posted comment %s for %s ref #%d", comment.ID, key, refID)
	return comment.ID, nil
}
