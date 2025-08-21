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

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/feedback/components/referendum"
	"github.com/stake-plus/govcomms/src/feedback/types"
	"gorm.io/gorm"
)

type ReplyMonitor struct {
	db                  *gorm.DB
	polkassemblyService *Service
	discord             *discordgo.Session
	logger              *log.Logger
}

type PolkassemblyComment struct {
	ID              string                `json:"id"`
	UserID          int                   `json:"userId"`
	Username        string                `json:"username"`
	Content         string                `json:"content"`
	CreatedAt       time.Time             `json:"createdAt"`
	UpdatedAt       time.Time             `json:"updatedAt"`
	ParentCommentID *string               `json:"parentCommentId"`
	IsDeleted       bool                  `json:"isDeleted"`
	PublicUser      *PolkassemblyUser     `json:"publicUser"`
	Children        []PolkassemblyComment `json:"children"`
}

type PolkassemblyUser struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
}

func NewReplyMonitor(db *gorm.DB, polkassemblyService *Service, discord *discordgo.Session, logger *log.Logger) *ReplyMonitor {
	return &ReplyMonitor{
		db:                  db,
		polkassemblyService: polkassemblyService,
		discord:             discord,
		logger:              logger,
	}
}

func (rm *ReplyMonitor) Start(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run immediately
	rm.checkForReplies()

	for {
		select {
		case <-ctx.Done():
			rm.logger.Println("Reply monitor stopping")
			return
		case <-ticker.C:
			rm.checkForReplies()
		}
	}
}

func (rm *ReplyMonitor) checkForReplies() {
	rm.logger.Println("Checking for Polkassembly replies...")

	// Get all unfinalized referenda where we have posted a comment to Polkassembly
	var messages []types.RefMessage
	err := rm.db.Where("internal = ? AND polkassembly_comment_id IS NOT NULL AND polkassembly_comment_id != ''", true).
		Find(&messages).Error

	if err != nil {
		rm.logger.Printf("Failed to get messages for reply check: %v", err)
		return
	}

	rm.logger.Printf("Found %d messages with Polkassembly comments to check", len(messages))

	// Group by ref_id to minimize API calls
	refMap := make(map[uint64]*types.RefMessage)
	for i := range messages {
		refMap[messages[i].RefID] = &messages[i]
	}

	for refID, msg := range refMap {
		// Get ref details
		var ref types.Ref
		if err := rm.db.First(&ref, refID).Error; err != nil {
			rm.logger.Printf("Failed to get ref %d: %v", refID, err)
			continue
		}

		// Skip if finalized
		if ref.Finalized {
			continue
		}

		// Rate limit check - only check every 2 minutes
		if ref.LastReplyCheck != nil && time.Since(*ref.LastReplyCheck) < 2*time.Minute {
			continue
		}

		rm.checkReferendumReplies(ref, msg)

		// Update last check time
		now := time.Now()
		rm.db.Model(&ref).Update("last_reply_check", &now)

		// Rate limit
		time.Sleep(2 * time.Second)
	}
}

func (rm *ReplyMonitor) checkReferendumReplies(ref types.Ref, ourMessage *types.RefMessage) {
	// Get network
	var network types.Network
	if err := rm.db.First(&network, ref.NetworkID).Error; err != nil {
		rm.logger.Printf("Failed to get network for ref %d: %v", ref.RefID, err)
		return
	}

	networkName := strings.ToLower(network.Name)

	if ourMessage.PolkassemblyCommentID == nil || *ourMessage.PolkassemblyCommentID == "" {
		return
	}

	rm.logger.Printf("Checking replies for %s ref %d (our comment ID: %s)", networkName, ref.RefID, *ourMessage.PolkassemblyCommentID)

	// Get all comments for this referendum from Polkassembly
	comments, err := rm.getComments(networkName, int(ref.RefID))
	if err != nil {
		rm.logger.Printf("Failed to get comments for %s ref %d: %v", networkName, ref.RefID, err)
		return
	}

	// Flatten the comment tree to process all comments
	flatComments := rm.flattenComments(comments)
	rm.logger.Printf("Found %d total comments (including nested) for %s ref %d", len(flatComments), networkName, ref.RefID)

	// Find replies to our comment
	var newReplies []PolkassemblyComment
	for _, comment := range flatComments {
		// Skip our own comment
		if comment.ID == *ourMessage.PolkassemblyCommentID {
			continue
		}

		// Check if this is a reply to our comment
		if comment.ParentCommentID != nil && *comment.ParentCommentID == *ourMessage.PolkassemblyCommentID {
			// Check if we already have this reply
			var existing types.RefMessage
			err := rm.db.Where("ref_id = ? AND polkassembly_comment_id = ?", ref.ID, comment.ID).First(&existing).Error
			if err == gorm.ErrRecordNotFound {
				newReplies = append(newReplies, comment)
				username := comment.Username
				if comment.PublicUser != nil && comment.PublicUser.Username != "" {
					username = comment.PublicUser.Username
				}
				rm.logger.Printf("Found new reply from %s (comment ID: %s)", username, comment.ID)
			}
		}
	}

	// Process new replies
	for _, reply := range newReplies {
		rm.processReply(ref, reply, network)
	}
}

func (rm *ReplyMonitor) flattenComments(comments []PolkassemblyComment) []PolkassemblyComment {
	var flat []PolkassemblyComment
	for _, comment := range comments {
		flat = append(flat, comment)
		if len(comment.Children) > 0 {
			flat = append(flat, rm.flattenComments(comment.Children)...)
		}
	}
	return flat
}

func (rm *ReplyMonitor) getComments(network string, refID int) ([]PolkassemblyComment, error) {
	// Use v2 API to get comments for a referendum
	url := fmt.Sprintf("https://%s.polkassembly.io/api/v2/ReferendumV2/%d/comments", network, refID)
	rm.logger.Printf("Fetching comments from: %s", url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Check if response is HTML (error page)
	bodyStr := string(body)
	if strings.HasPrefix(strings.TrimSpace(bodyStr), "<") {
		rm.logger.Printf("Got HTML response instead of JSON from %s", url)
		return []PolkassemblyComment{}, nil
	}

	// Parse the response - the API returns an array of comments with nested children
	var comments []PolkassemblyComment
	if err := json.Unmarshal(body, &comments); err != nil {
		rm.logger.Printf("Failed to parse comments response: %v", err)
		return []PolkassemblyComment{}, nil
	}

	return comments, nil
}

func (rm *ReplyMonitor) processReply(ref types.Ref, reply PolkassemblyComment, network types.Network) {
	username := reply.Username
	if reply.PublicUser != nil && reply.PublicUser.Username != "" {
		username = reply.PublicUser.Username
	}

	rm.logger.Printf("Processing reply from %s for %s ref %d", username, network.Name, ref.RefID)

	// Store the reply in database
	msg := types.RefMessage{
		RefID:                 ref.ID,
		Author:                username,
		Body:                  reply.Content,
		Internal:              false,
		PolkassemblyCommentID: &reply.ID,
		PolkassemblyUsername:  username,
		CreatedAt:             reply.CreatedAt,
	}

	if err := rm.db.Create(&msg).Error; err != nil {
		rm.logger.Printf("Failed to save reply: %v", err)
		return
	}

	// Find the Discord thread for this referendum
	threadInfo, err := referendum.GetThreadByRef(rm.db, network.ID, uint32(ref.RefID))
	if err != nil {
		rm.logger.Printf("Failed to find Discord thread for %s ref %d: %v", network.Name, ref.RefID, err)
		return
	}

	if threadInfo == nil || threadInfo.ThreadID == "" {
		rm.logger.Printf("No Discord thread found for %s ref %d", network.Name, ref.RefID)
		return
	}

	// Post the reply to Discord
	rm.postReplyToDiscord(threadInfo.ThreadID, reply, network.Name, ref.RefID)
}

func (rm *ReplyMonitor) postReplyToDiscord(threadID string, reply PolkassemblyComment, networkName string, refID uint64) {
	// Get username from publicUser if available
	username := reply.Username
	if reply.PublicUser != nil && reply.PublicUser.Username != "" {
		username = reply.PublicUser.Username
	}

	// Clean up the content - remove excessive markdown/HTML if present
	content := strings.TrimSpace(reply.Content)

	// Truncate if too long
	maxLength := 1800
	if len(content) > maxLength {
		content = content[:maxLength-3] + "..."
	}

	// Create embed for the reply
	embed := &discordgo.MessageEmbed{
		Author: &discordgo.MessageEmbedAuthor{
			Name:    fmt.Sprintf("Reply from %s via Polkassembly", username),
			IconURL: "https://polkassembly.io/favicon.ico",
		},
		Description: content,
		Color:       0x00bfa5, // Teal color for external replies
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Polkassembly • %s Ref #%d", networkName, refID),
		},
		Timestamp: reply.CreatedAt.Format(time.RFC3339),
	}

	// Add link to the specific comment
	polkassemblyURL := fmt.Sprintf("https://%s.polkassembly.io/referenda/%d#%s", strings.ToLower(networkName), refID, reply.ID)
	embed.Fields = []*discordgo.MessageEmbedField{
		{
			Name:   "View on Polkassembly",
			Value:  fmt.Sprintf("[Direct link to reply](%s)", polkassemblyURL),
			Inline: false,
		},
	}

	// Send to Discord thread
	_, err := rm.discord.ChannelMessageSendEmbed(threadID, embed)
	if err != nil {
		rm.logger.Printf("Failed to post reply to Discord thread %s: %v", threadID, err)
		return
	}

	rm.logger.Printf("Posted reply from %s to Discord thread %s", username, threadID)
}
