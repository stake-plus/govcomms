package polkassembly

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/stake-plus/govcomms/src/bot/data"
	"gorm.io/gorm"
)

type Service struct {
	clients      map[string]*Client
	logger       *log.Logger
	db           *gorm.DB
	replyMonitor *ReplyMonitor
}

type CommentResponse struct {
	ID      int    `json:"id"`
	Content string `json:"content"`
	UserID  int    `json:"user_id"`
}

func NewService(logger *log.Logger, db *gorm.DB) (*Service, error) {
	endpoint := os.Getenv("POLKASSEMBLY_ENDPOINT")
	if endpoint == "" {
		endpoint = "https://api.polkassembly.io/api/v2"
	}

	clients := make(map[string]*Client)

	polkadotSeed := os.Getenv("POLKASSEMBLY_POLKADOT_SEED")
	if polkadotSeed != "" {
		logger.Printf("Creating Polkadot signer from seed...")
		signer, err := NewPolkadotSignerFromSeed(polkadotSeed, 0)
		if err != nil {
			return nil, fmt.Errorf("create polkadot signer: %w", err)
		}
		clients["polkadot"] = NewClient(endpoint, signer)
		logger.Printf("Polkassembly client configured for Polkadot with address: %s", signer.Address())
	}

	kusamaSeed := os.Getenv("POLKASSEMBLY_KUSAMA_SEED")
	if kusamaSeed != "" {
		logger.Printf("Creating Kusama signer from seed...")
		signer, err := NewPolkadotSignerFromSeed(kusamaSeed, 2)
		if err != nil {
			return nil, fmt.Errorf("create kusama signer: %w", err)
		}
		clients["kusama"] = NewClient(endpoint, signer)
		logger.Printf("Polkassembly client configured for Kusama with address: %s", signer.Address())
	}

	if len(clients) == 0 {
		genericSeed := os.Getenv("POLKASSEMBLY_SEED")
		if genericSeed == "" {
			return nil, fmt.Errorf("no POLKASSEMBLY_SEED environment variables set")
		}

		logger.Printf("Using generic seed for both networks...")
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
	}

	service := &Service{
		clients: clients,
		logger:  logger,
		db:      db,
	}

	// Create reply monitor
	service.replyMonitor = NewReplyMonitor(db, clients, logger)

	return service, nil
}

func (s *Service) PostFirstMessage(network string, refID int, message string) (string, error) { // Changed return type to string
	s.logger.Printf("PostFirstMessage called for %s referendum #%d", network, refID)

	networkLower := strings.ToLower(network)
	client, exists := s.clients[networkLower]
	if !exists {
		return "", fmt.Errorf("no Polkassembly client configured for network %s", network)
	}

	var intro, outro string
	if s.db != nil {
		if err := data.LoadSettings(s.db); err != nil {
			s.logger.Printf("Failed to load settings: %v", err)
		}
		intro = data.GetSetting("polkassembly_intro")
		outro = data.GetSetting("polkassembly_outro")
	}

	if intro == "" {
		intro = "## 🏛️ REEEEEEEEEE DAO\n\nREEEEEEEEEE DAO is a decentralized collective body committed to serve Polkadot Opengov. Our mission is to provide high-quality assessments on referenda to ensure outcomes that strengthen the Polkadot ecosystem. Each referendum is reviewed carefully by our DAO members through the scope of technical, strategic, and governance.\n\n### 📋 Community Feedback"
	}

	if outro == "" {
		outro = "\n\n### 💬 Open Communication Channel\n\nFor further discussion and detailed feedback, please reply to this comment.\n\n*This feedback represents the collective voice of REEEEEEEEEE DAO members participating in Polkadot governance.*"
	}

	indentedFeedback := "> " + strings.ReplaceAll(message, "\n", "\n> ")
	content := fmt.Sprintf("%s\n\n%s\n%s", intro, indentedFeedback, outro)

	s.logger.Printf("Attempting to post comment to Polkassembly for %s #%d", network, refID)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	responseBody, err := client.PostCommentWithResponse(ctx, content, refID, networkLower)
	if err != nil {
		s.logger.Printf("Error posting to Polkassembly: %v", err)
		return "", fmt.Errorf("post comment: %w", err)
	}

	// Log raw response for debugging
	s.logger.Printf("Raw Polkassembly response: %s", string(responseBody))

	// Parse the response - Polkassembly v2 API returns string IDs
	var commentID string

	// Parse the response
	var response struct {
		ID              string    `json:"id"`
		Network         string    `json:"network"`
		ProposalType    string    `json:"proposalType"`
		UserID          int       `json:"userId"`
		Content         string    `json:"content"`
		CreatedAt       time.Time `json:"createdAt"`
		UpdatedAt       time.Time `json:"updatedAt"`
		IsDeleted       bool      `json:"isDeleted"`
		IndexOrHash     string    `json:"indexOrHash"`
		ParentCommentID *string   `json:"parentCommentId"`
		DataSource      string    `json:"dataSource"`
		AISentiment     string    `json:"aiSentiment"`
	}

	if err := json.Unmarshal(responseBody, &response); err != nil {
		s.logger.Printf("Failed to parse Polkassembly response: %v", err)
		return "", fmt.Errorf("parse response: %w", err)
	}

	commentID = response.ID

	if commentID == "" {
		s.logger.Printf("WARNING: No comment ID in Polkassembly response")
		return "", fmt.Errorf("no comment ID in response")
	}

	s.logger.Printf("Successfully posted first message to Polkassembly for %s referendum #%d with comment ID %s",
		network, refID, commentID)

	return commentID, nil
}

// Add method to find our comment
func (s *Service) findOurComment(network string, refID int) int {
	url := fmt.Sprintf("https://%s.polkassembly.io/api/v1/posts/on-chain-post/%d/comments", network, refID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		s.logger.Printf("Failed to create request: %v", err)
		return 0
	}

	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Printf("Failed to get comments: %v", err)
		return 0
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Printf("Failed to read response: %v", err)
		return 0
	}

	s.logger.Printf("Recovery attempt response: %s", string(body))

	// Check if it's JSON
	if strings.HasPrefix(strings.TrimSpace(string(body)), "<") {
		s.logger.Printf("Got HTML response during recovery")
		return 0
	}

	// Try different response formats
	// Format 1: Array of comments
	var comments []struct {
		ID       int    `json:"id"`
		Content  string `json:"content"`
		Username string `json:"username"`
	}

	if err := json.Unmarshal(body, &comments); err == nil {
		for _, comment := range comments {
			if strings.Contains(comment.Content, "REEEEEEEEEE DAO") {
				s.logger.Printf("Found our comment with ID %d", comment.ID)
				return comment.ID
			}
		}
	}

	// Format 2: Object with comments array
	var response struct {
		Comments []struct {
			ID       int    `json:"id"`
			Content  string `json:"content"`
			Username string `json:"username"`
		} `json:"comments"`
	}

	if err := json.Unmarshal(body, &response); err == nil {
		for _, comment := range response.Comments {
			if strings.Contains(comment.Content, "REEEEEEEEEE DAO") {
				s.logger.Printf("Found our comment with ID %d", comment.ID)
				return comment.ID
			}
		}
	}

	s.logger.Printf("Could not find our comment in recovery attempt")
	return 0
}

func (s *Service) StartReplyMonitor(ctx context.Context, interval time.Duration) {
	go s.replyMonitor.Start(ctx, interval)
}
