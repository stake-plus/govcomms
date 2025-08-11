package referendum

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/bot/components/network"
	"github.com/stake-plus/govcomms/src/bot/types"
	"gorm.io/gorm"
)

type ThreadInfo struct {
	ThreadID  string
	NetworkID uint8
	RefID     uint64
	RefDBID   uint64
}

type Manager struct {
	db       *gorm.DB
	networks *network.Manager
	threads  map[string]*ThreadInfo
	mu       sync.RWMutex
}

func NewManager(db *gorm.DB, networks *network.Manager) *Manager {
	return &Manager{
		db:       db,
		networks: networks,
		threads:  make(map[string]*ThreadInfo),
	}
}

func (m *Manager) GetThreadInfo(threadID string) *ThreadInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.threads[threadID]
}

func (m *Manager) SyncThreads(s *discordgo.Session, guildID string) error {
	guild, err := s.Guild(guildID)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.threads = make(map[string]*ThreadInfo)

	for _, channel := range guild.Channels {
		if channel.Type == discordgo.ChannelTypeGuildPublicThread ||
			channel.Type == discordgo.ChannelTypeGuildPrivateThread {

			// Parse thread name for referendum info
			if info := m.parseThreadName(channel.Name, channel.ParentID); info != nil {
				// Get the database ID for this referendum
				var ref types.Ref
				if err := m.db.Where("network_id = ? AND ref_id = ?",
					info.NetworkID, info.RefID).First(&ref).Error; err == nil {
					info.RefDBID = ref.ID
					info.ThreadID = channel.ID
					m.threads[channel.ID] = info
				}
			}
		}
	}

	log.Printf("Synced %d referendum threads", len(m.threads))
	return nil
}

func (m *Manager) parseThreadName(name string, parentID string) *ThreadInfo {
	// Look for pattern like "Referendum #123" or "Ref #123"
	parts := strings.Split(strings.ToLower(name), "#")
	if len(parts) < 2 {
		return nil
	}

	// Extract referendum number
	numStr := strings.TrimSpace(strings.Split(parts[1], " ")[0])
	refID, err := strconv.ParseUint(numStr, 10, 64)
	if err != nil {
		return nil
	}

	// Find network by parent channel
	net := m.networks.FindByChannelID(parentID)
	if net == nil {
		return nil
	}

	return &ThreadInfo{
		NetworkID: net.ID,
		RefID:     refID,
	}
}

func (m *Manager) FindThread(s *discordgo.Session, guildID, channelID string, refID int) (*discordgo.Channel, error) {
	threads, err := s.GuildThreadsActive(guildID)
	if err != nil {
		return nil, err
	}

	refStr := fmt.Sprintf("#%d", refID)
	for _, thread := range threads.Threads {
		if thread.ParentID == channelID && strings.Contains(thread.Name, refStr) {
			return thread, nil
		}
	}

	return nil, fmt.Errorf("thread not found for referendum %d", refID)
}

func (m *Manager) HandleThreadUpdate(t *discordgo.ThreadUpdate) {
	if info := m.parseThreadName(t.Name, t.ParentID); info != nil {
		var ref types.Ref
		if err := m.db.Where("network_id = ? AND ref_id = ?",
			info.NetworkID, info.RefID).First(&ref).Error; err == nil {
			info.RefDBID = ref.ID
			info.ThreadID = t.ID

			m.mu.Lock()
			m.threads[t.ID] = info
			m.mu.Unlock()
		}
	}
}
