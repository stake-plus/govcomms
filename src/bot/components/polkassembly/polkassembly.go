package polkassembly

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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

	// Try different response formats since Polkassembly API might return different structures
	// First try the expected format
	var response struct {
		ID      int `json:"id"`
		Comment struct {
			ID int `json:"id"`
		} `json:"comment"`
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}

	if err := json.Unmarshal(responseBody, &response); err != nil {
		s.logger.Printf("Failed to parse response as expected format: %v", err)
		s.logger.Printf("Raw response: %s", string(responseBody))

		// Try to extract ID from any JSON response that has an "id" field
		var genericResponse map[string]interface{}
		if err := json.Unmarshal(responseBody, &genericResponse); err == nil {
			// Check for id in various places
			if id, ok := genericResponse["id"].(float64); ok {
				s.logger.Printf("Found comment ID in root: %d", int(id))
				return int(id), nil
			}
			if comment, ok := genericResponse["comment"].(map[string]interface{}); ok {
				if id, ok := comment["id"].(float64); ok {
					s.logger.Printf("Found comment ID in comment object: %d", int(id))
					return int(id), nil
				}
			}
			if data, ok := genericResponse["data"].(map[string]interface{}); ok {
				if id, ok := data["id"].(float64); ok {
					s.logger.Printf("Found comment ID in data object: %d", int(id))
					return int(id), nil
				}
			}
		}

		s.logger.Printf("Could not extract comment ID from response, but comment was likely posted")
		return 0, nil
	}

	// Check which field has the ID
	commentID := response.ID
	if commentID == 0 && response.Comment.ID > 0 {
		commentID = response.Comment.ID
	}
	if commentID == 0 && response.Data.ID > 0 {
		commentID = response.Data.ID
	}

	s.logger.Printf("Successfully posted first message to Polkassembly for %s referendum #%d with comment ID %d",
		network, refID, commentID)

	return commentID, nil
}

func (s *Service) StartReplyMonitor(ctx context.Context, interval time.Duration) {
	go s.replyMonitor.Start(ctx, interval)
}
