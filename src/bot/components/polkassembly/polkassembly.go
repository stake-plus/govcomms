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
func (s *Service) PostFirstMessage(network string, refID int, message string) (int, error) {
	s.logger.Printf("PostFirstMessage called for %s referendum #%d", network, refID)

	networkLower := strings.ToLower(network)
	client, exists := s.clients[networkLower]
	if !exists {
		return 0, fmt.Errorf("no Polkassembly client configured for network %s", network)
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
		return 0, fmt.Errorf("post comment: %w", err)
	}

	// Log raw response for debugging
	s.logger.Printf("Raw Polkassembly response: %s", string(responseBody))

	// Try to parse the response - Polkassembly v2 API might return different formats
	var commentID int

	// First try: Direct comment object
	var directComment struct {
		ID      int    `json:"id"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(responseBody, &directComment); err == nil && directComment.ID > 0 {
		commentID = directComment.ID
		s.logger.Printf("Found comment ID from direct response: %d", commentID)
	}

	// Second try: Wrapped in data field
	if commentID == 0 {
		var wrappedResponse struct {
			Data struct {
				ID      int    `json:"id"`
				Content string `json:"content"`
			} `json:"data"`
		}
		if err := json.Unmarshal(responseBody, &wrappedResponse); err == nil && wrappedResponse.Data.ID > 0 {
			commentID = wrappedResponse.Data.ID
			s.logger.Printf("Found comment ID from data field: %d", commentID)
		}
	}

	// Third try: Comment field
	if commentID == 0 {
		var commentResponse struct {
			Comment struct {
				ID      int    `json:"id"`
				Content string `json:"content"`
			} `json:"comment"`
		}
		if err := json.Unmarshal(responseBody, &commentResponse); err == nil && commentResponse.Comment.ID > 0 {
			commentID = commentResponse.Comment.ID
			s.logger.Printf("Found comment ID from comment field: %d", commentID)
		}
	}

	// Fourth try: Generic map to find any ID field
	if commentID == 0 {
		var genericResponse map[string]interface{}
		if err := json.Unmarshal(responseBody, &genericResponse); err == nil {
			s.logger.Printf("Generic response map: %+v", genericResponse)

			// Check for id at root
			if id, ok := genericResponse["id"].(float64); ok {
				commentID = int(id)
				s.logger.Printf("Found comment ID from root: %d", commentID)
			}

			// Check nested objects
			for key, value := range genericResponse {
				if obj, ok := value.(map[string]interface{}); ok {
					if id, ok := obj["id"].(float64); ok {
						commentID = int(id)
						s.logger.Printf("Found comment ID from %s.id: %d", key, commentID)
						break
					}
				}
			}
		}
	}

	if commentID == 0 {
		s.logger.Printf("WARNING: Could not extract comment ID from Polkassembly response")
		s.logger.Printf("Response was: %s", string(responseBody))

		// Try to recover the comment ID immediately
		s.logger.Printf("Attempting to recover comment ID for %s ref %d", network, refID)
		time.Sleep(5 * time.Second) // Wait a bit for Polkassembly to process

		if recoveredID := s.findOurComment(networkLower, refID); recoveredID > 0 {
			commentID = recoveredID
			s.logger.Printf("Successfully recovered comment ID: %d", commentID)
		}
	}

	s.logger.Printf("Final comment ID for %s referendum #%d: %d", network, refID, commentID)
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
