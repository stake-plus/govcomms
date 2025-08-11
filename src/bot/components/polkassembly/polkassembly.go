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
		intro = "## 🏛️ REEEEEEEEEE DAO\n\nREEEEEEEEEE DAO is a decentralized collective body committed to serve Polkadot Opengov. Our mission is to provide high-quality assessments on referenda to ensure outcomes that strengthen the Polkadot ecosystem. Each referendum is reviewed carefully by our DAO members through the scope of technical, strategic, and governance. \n\n### 📋 Community Feedback"
	}

	if outro == "" {
		outro = "\n\n### 💬 Open Communication Channel\n\nFor further discussion and detailed feedback, please reply to this comment.\n\n*This feedback represents the collective voice of REEEEEEEEEE DAO members participating in Polkadot governance.*"
	}

	indentedFeedback := "> " + strings.ReplaceAll(message, "\n", "\n> ")
	content := fmt.Sprintf("%s\n\n%s\n%s", intro, indentedFeedback, outro)

	s.logger.Printf("Attempting to post comment to Polkassembly for %s #%d", network, refID)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	responseBody, err := client.PostCommentWithResponse(ctx, content, refID, networkLower)
	if err != nil {
		s.logger.Printf("Error posting to Polkassembly: %v", err)
		return 0, fmt.Errorf("post comment: %w", err)
	}

	// Parse response to get comment ID
	var response CommentResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		s.logger.Printf("Failed to parse response but comment may have been posted: %v", err)
		return 0, nil
	}

	s.logger.Printf("Successfully posted first message to Polkassembly for %s referendum #%d with comment ID %d",
		network, refID, response.ID)

	return response.ID, nil
}

func (s *Service) StartReplyMonitor(ctx context.Context, interval time.Duration) {
	go s.replyMonitor.Start(ctx, interval)
}
