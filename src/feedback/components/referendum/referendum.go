package referendum

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/feedback/config"
	"github.com/stake-plus/govcomms/src/feedback/types"
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

	var network types.Network
	if err := h.db.First(&network, networkID).Error; err != nil {
		log.Printf("Network %d not found: %v", networkID, err)
		return
	}

	var ref types.Ref
	if err := h.db.Where("network_id = ? AND ref_id = ?", networkID, refID).First(&ref).Error; err != nil {
		log.Printf("Referendum %s #%d not found: %v", network.Name, refID, err)
		return
	}

	var refThread types.RefThread
	err = h.db.Where("thread_id = ?", thread.ID).First(&refThread).Error
	if err == gorm.ErrRecordNotFound {
		refThread = types.RefThread{
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
	var network types.Network
	err = h.db.Where("discord_channel_id = ?", parentChannelID).First(&network).Error
	if err != nil {
		return 0, 0, fmt.Errorf("no network found for channel %s", parentChannelID)
	}
	networkID = network.ID

	// Extract referendum number from title
	// Title format: "1711: [PULLED - Watch out for MEDIUM PRESSURE proposal]"
	// Extract the number at the beginning
	parts := strings.SplitN(title, ":", 2)
	if len(parts) == 0 {
		return 0, 0, fmt.Errorf("no referendum number found in title: %s", title)
	}

	// Try to parse the first part as a number
	refNumStr := strings.TrimSpace(parts[0])
	refNum, err := strconv.ParseUint(refNumStr, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid referendum number in title: %s", title)
	}

	return networkID, uint32(refNum), nil
}

type ThreadInfo struct {
	ThreadID  string
	RefID     uint64
	RefDBID   uint64
	NetworkID uint8
}

type Manager struct {
	db *gorm.DB
}

func NewManager(db *gorm.DB) *Manager {
	return &Manager{db: db}
}

func (m *Manager) FindThread(threadID string) (*ThreadInfo, error) {
	var refThread types.RefThread
	err := m.db.Where("thread_id = ?", threadID).First(&refThread).Error
	if err != nil {
		return nil, err
	}

	return &ThreadInfo{
		ThreadID:  refThread.ThreadID,
		RefID:     refThread.RefID,
		RefDBID:   refThread.RefDBID,
		NetworkID: refThread.NetworkID,
	}, nil
}

func (m *Manager) GetThreadInfo(networkID uint8, refID uint32) (*ThreadInfo, error) {
	var refThread types.RefThread
	err := m.db.Where("network_id = ? AND ref_id = ?", networkID, refID).First(&refThread).Error
	if err != nil {
		return nil, err
	}

	return &ThreadInfo{
		ThreadID:  refThread.ThreadID,
		RefID:     refThread.RefID,
		RefDBID:   refThread.RefDBID,
		NetworkID: refThread.NetworkID,
	}, nil
}
