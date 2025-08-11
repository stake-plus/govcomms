package polkassembly

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/stake-plus/govcomms/src/bot/data"
	"github.com/stake-plus/govcomms/src/bot/types"
	"gorm.io/gorm"
)

type Service struct {
	logger  *log.Logger
	clients map[string]*Client
	db      *gorm.DB
}

func NewService(logger *log.Logger, db *gorm.DB) (*Service, error) {
	clients := make(map[string]*Client)

	if err := data.LoadSettings(db); err != nil {
		return nil, fmt.Errorf("failed to load settings: %w", err)
	}

	// Get all networks from database
	var networks []types.Network
	if err := db.Find(&networks).Error; err != nil {
		return nil, fmt.Errorf("failed to load networks: %w", err)
	}

	// Check for credentials for each network
	for _, network := range networks {
		networkLower := strings.ToLower(network.Name)

		mnemonic := data.GetSetting(fmt.Sprintf("polkassembly_%s_mnemonic", networkLower))

		if mnemonic != "" {
			var ss58Format uint16
			if network.ID == 1 { // Polkadot in DB
				ss58Format = 0 // Polkadot SS58 format
			} else {
				ss58Format = uint16(network.ID) // Kusama and others use same ID
			}
			signer, _ := NewPolkadotSignerFromSeed(mnemonic, ss58Format)
			client := NewClient(networkLower, signer)
			clients[networkLower] = client
			logger.Printf("Polkassembly enabled for %s (address: %s)", network.Name, signer.Address())
		}
	}

	if len(clients) == 0 {
		return nil, fmt.Errorf("no Polkassembly credentials configured")
	}

	service := &Service{
		logger:  logger,
		clients: clients,
		db:      db,
	}

	return service, nil
}

func (s *Service) PostFirstMessage(network string, refID int, message string) (string, error) {
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

	s.logger.Printf("Raw Polkassembly response: %s", string(responseBody))

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

	commentID := response.ID

	if commentID == "" {
		s.logger.Printf("WARNING: No comment ID in Polkassembly response")
		return "", fmt.Errorf("no comment ID in response")
	}

	s.logger.Printf("Successfully posted first message to Polkassembly for %s referendum #%d with comment ID %s",
		network, refID, commentID)

	return commentID, nil
}

func (s *Service) PostReply(network string, refID int, parentCommentID string, message string) (string, error) {
	s.logger.Printf("PostReply called for %s referendum #%d", network, refID)

	networkLower := strings.ToLower(network)
	client, exists := s.clients[networkLower]
	if !exists {
		return "", fmt.Errorf("no Polkassembly client configured for network %s", network)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	responseBody, err := client.PostCommentWithResponse(ctx, message, refID, networkLower)
	if err != nil {
		s.logger.Printf("Error posting reply to Polkassembly: %v", err)
		return "", fmt.Errorf("post reply: %w", err)
	}

	var response struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal(responseBody, &response); err != nil {
		s.logger.Printf("Failed to parse reply response: %v", err)
		return "", fmt.Errorf("parse reply response: %w", err)
	}

	return response.ID, nil
}

func (s *Service) findOurComment(network string, refID int) (string, error) {
	networkLower := strings.ToLower(network)
	url := fmt.Sprintf("https://%s.polkassembly.io/api/v2/posts/on-chain-post?proposalType=referendums_v2&postId=%d", networkLower, refID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var response struct {
		Comments []struct {
			ID       string `json:"id"`
			UserID   int    `json:"userId"`
			Username string `json:"username"`
			Content  string `json:"content"`
		} `json:"comments"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return "", err
	}

	ourUsername := data.GetSetting("polkassembly_username")
	if ourUsername == "" {
		ourUsername = "REEEEEEEEEEDAO"
	}

	for _, comment := range response.Comments {
		if comment.Username == ourUsername {
			return comment.ID, nil
		}
	}

	return "", fmt.Errorf("comment not found")
}
