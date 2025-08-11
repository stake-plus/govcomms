package referendum

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/bot/config"
	"github.com/stake-plus/govcomms/src/bot/types"
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

	networkID, refID, err := h.parseThreadTitle(thread.Name)
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

func (h *Handler) parseThreadTitle(title string) (networkID uint8, refID uint32, err error) {
	polkadotPattern := regexp.MustCompile(`(?i)(?:polkadot|dot)\s*#?\s*(\d+)`)
	kusamaPattern := regexp.MustCompile(`(?i)(?:kusama|ksm)\s*#?\s*(\d+)`)

	title = strings.ToLower(title)

	if matches := polkadotPattern.FindStringSubmatch(title); matches != nil {
		networkID = 1
		ref, _ := strconv.ParseUint(matches[1], 10, 32)
		refID = uint32(ref)
		return networkID, refID, nil
	}

	if matches := kusamaPattern.FindStringSubmatch(title); matches != nil {
		networkID = 2
		ref, _ := strconv.ParseUint(matches[1], 10, 32)
		refID = uint32(ref)
		return networkID, refID, nil
	}

	return 0, 0, fmt.Errorf("no referendum found in title: %s", title)
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
