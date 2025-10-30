package referendum

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/feedback/config"
	"github.com/stake-plus/govcomms/src/feedback/types"
	sharedgov "github.com/stake-plus/govcomms/src/shared/gov"
	"gorm.io/gorm"
)

type Handler struct {
	db     *gorm.DB
	config *config.Config
}

func NewHandler(db *gorm.DB, config *config.Config) *Handler {
	return &Handler{
		db:     db,
		config: config,
	}
}

func (h *Handler) HandleThreadCreate(s *discordgo.Session, t *discordgo.ThreadCreate) {
	h.processThread(s, t.Channel)
}

func (h *Handler) HandleThreadUpdate(s *discordgo.Session, t *discordgo.ThreadUpdate) {
	h.processThread(s, t.Channel)
}

func (h *Handler) processThread(s *discordgo.Session, thread *discordgo.Channel) {
	if thread.Type != discordgo.ChannelTypeGuildPublicThread {
		return
	}

	networkID, refID, err := h.parseThreadTitle(thread.Name, thread.ParentID)
	if err != nil {
		return
	}

	var network sharedgov.Network
	if err := h.db.First(&network, networkID).Error; err != nil {
		log.Printf("Network %d not found: %v", networkID, err)
		return
	}

	var ref types.Ref
	if err := h.db.Where("network_id = ? AND ref_id = ?", networkID, refID).First(&ref).Error; err != nil {
		log.Printf("Referendum %s #%d not found: %v", network.Name, refID, err)
		return
	}

	var refThread sharedgov.RefThread
	err = h.db.Where("thread_id = ?", thread.ID).First(&refThread).Error
	if err == gorm.ErrRecordNotFound {
		refThread = sharedgov.RefThread{
			ThreadID:  thread.ID,
			RefDBID:   ref.ID,
			NetworkID: networkID,
			RefID:     uint64(refID),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		if err := h.db.Create(&refThread).Error; err != nil {
			log.Printf("Failed to create thread mapping: %v", err)
		} else {
			log.Printf("Created thread mapping: %s -> %s ref #%d", thread.ID, network.Name, refID)
		}
	} else if err == nil {
		if err := h.db.Model(&refThread).Update("updated_at", time.Now()).Error; err != nil {
			log.Printf("Failed to update thread mapping: %v", err)
		} else {
			log.Printf("Updated thread %s -> %s ref #%d", thread.ID, network.Name, refID)
		}
	}
}

func (h *Handler) parseThreadTitle(title string, parentChannelID string) (networkID uint8, refID uint32, err error) {
	// First determine network from parent channel
	var network sharedgov.Network
	err = h.db.Where("discord_channel_id = ?", parentChannelID).First(&network).Error
	if err != nil {
		return 0, 0, fmt.Errorf("no network found for channel %s", parentChannelID)
	}
	networkID = network.ID

	// Extract referendum number from title using regex to handle special characters
	// Look for a number at the beginning, possibly with quotes or other characters
	re := regexp.MustCompile(`^\s*["']?(\d+)\s*["']?\s*:`)
	matches := re.FindStringSubmatch(title)

	if len(matches) < 2 {
		// Fallback: try to find any number followed by colon
		re = regexp.MustCompile(`(\d+)\s*:`)
		matches = re.FindStringSubmatch(title)

		if len(matches) < 2 {
			// Last resort: find first number in the title
			re = regexp.MustCompile(`(\d+)`)
			matches = re.FindStringSubmatch(title)

			if len(matches) < 2 {
				return 0, 0, fmt.Errorf("no referendum number found in title: %s", title)
			}
		}
	}

	refNumStr := matches[1]
	refNum, err := strconv.ParseUint(refNumStr, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid referendum number in title: %s", title)
	}

	return networkID, uint32(refNum), nil
}

// Manager and ThreadInfo are now in shared/gov - use sharedgov.ReferendumManager and sharedgov.ThreadInfo
