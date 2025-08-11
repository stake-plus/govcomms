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
	log.Printf("Starting thread sync for guild %s", guildID)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.threads = make(map[string]*ThreadInfo)

	// Get all networks
	networks := m.networks.GetAll()

	for netID, network := range networks {
		if network.DiscordChannelID == "" {
			continue
		}

		log.Printf("Syncing threads for %s (channel: %s)", network.Name, network.DiscordChannelID)

		// Get threads for this network's forum channel
		threads, err := s.ThreadsActive(network.DiscordChannelID)
		if err != nil {
			log.Printf("Failed to get threads for %s: %v", network.Name, err)
			continue
		}

		for _, thread := range threads.Threads {
			// Extract ref ID from thread name (format: "498: title")
			parts := strings.SplitN(thread.Name, ":", 2)
			if len(parts) < 1 {
				continue
			}

			refID, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
			if err != nil {
				continue
			}

			// Check if this referendum exists in database
			var ref types.Ref
			if err := m.db.Where("network_id = ? AND ref_id = ?", netID, refID).First(&ref).Error; err != nil {
				if err != gorm.ErrRecordNotFound {
					log.Printf("Database error for %s ref %d: %v", network.Name, refID, err)
				}
				continue
			}

			// Store thread mapping
			info := &ThreadInfo{
				ThreadID:  thread.ID,
				NetworkID: netID,
				RefID:     refID,
				RefDBID:   ref.ID,
			}

			m.threads[thread.ID] = info
			log.Printf("Synced thread: %s -> %s ref #%d", thread.ID, network.Name, refID)
		}

		// Also check archived threads
		archived, err := s.ThreadsArchived(network.DiscordChannelID, nil, 100)
		if err == nil {
			for _, thread := range archived.Threads {
				// Skip if already mapped
				if _, exists := m.threads[thread.ID]; exists {
					continue
				}

				// Extract ref ID
				parts := strings.SplitN(thread.Name, ":", 2)
				if len(parts) < 1 {
					continue
				}

				refID, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
				if err != nil {
					continue
				}

				// Check database
				var ref types.Ref
				if err := m.db.Where("network_id = ? AND ref_id = ?", netID, refID).First(&ref).Error; err != nil {
					continue
				}

				// Store mapping
				info := &ThreadInfo{
					ThreadID:  thread.ID,
					NetworkID: netID,
					RefID:     refID,
					RefDBID:   ref.ID,
				}

				m.threads[thread.ID] = info
				log.Printf("Synced archived thread: %s -> %s ref #%d", thread.ID, network.Name, refID)
			}
		}
	}

	log.Printf("Thread sync complete: %d threads synced", len(m.threads))
	return nil
}

func (m *Manager) FindThread(s *discordgo.Session, guildID, channelID string, refID int) (*discordgo.Channel, error) {
	// Get threads from the forum channel
	threads, err := s.ThreadsActive(channelID)
	if err != nil {
		return nil, err
	}

	refPrefix := fmt.Sprintf("%d:", refID)

	for _, thread := range threads.Threads {
		if strings.HasPrefix(strings.TrimSpace(thread.Name), refPrefix) {
			return thread, nil
		}
	}

	// Check archived threads
	archived, err := s.ThreadsArchived(channelID, nil, 100)
	if err == nil {
		for _, thread := range archived.Threads {
			if strings.HasPrefix(strings.TrimSpace(thread.Name), refPrefix) {
				return thread, nil
			}
		}
	}

	return nil, fmt.Errorf("thread not found for referendum %d", refID)
}

func (m *Manager) HandleThreadUpdate(t *discordgo.ThreadUpdate) {
	// Extract ref ID from thread name
	parts := strings.SplitN(t.Name, ":", 2)
	if len(parts) < 1 {
		return
	}

	refID, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return
	}

	// Find network by channel
	net := m.networks.FindByChannelID(t.ParentID)
	if net == nil {
		return
	}

	// Check database
	var ref types.Ref
	if err := m.db.Where("network_id = ? AND ref_id = ?", net.ID, refID).First(&ref).Error; err != nil {
		return
	}

	// Update mapping
	info := &ThreadInfo{
		ThreadID:  t.ID,
		NetworkID: net.ID,
		RefID:     refID,
		RefDBID:   ref.ID,
	}

	m.mu.Lock()
	m.threads[t.ID] = info
	m.mu.Unlock()

	log.Printf("Updated thread %s -> %s ref #%d", t.ID, net.Name, refID)
}
