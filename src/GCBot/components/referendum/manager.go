package referendum

import (
	"fmt"
	"log"
	"regexp"
	"strconv"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/GCApi/types"
	"github.com/stake-plus/govcomms/src/GCBot/components/network"
	"gorm.io/gorm"
)

type ThreadInfo struct {
	ThreadID  string
	NetworkID uint8
	RefID     uint64
	RefDBID   uint64 // Database ID of the referendum
}

type Manager struct {
	db            *gorm.DB
	networks      *network.Manager
	threadMapping map[string]*ThreadInfo
}

func NewManager(db *gorm.DB, networks *network.Manager) *Manager {
	return &Manager{
		db:            db,
		networks:      networks,
		threadMapping: make(map[string]*ThreadInfo),
	}
}

func (m *Manager) HandleThreadUpdate(t *discordgo.ThreadUpdate) {
	net := m.networks.FindByChannelID(t.ParentID)
	if net == nil {
		return
	}

	refID := m.ExtractRefID(t.Name)
	if refID > 0 {
		log.Printf("Detected referendum thread: %s (Ref #%d) in %s channel",
			t.Name, refID, net.Name)

		// Update thread mapping
		m.UpdateThreadMapping(t.ID, net.ID, refID)
	}
}

func (m *Manager) UpdateThreadMapping(threadID string, networkID uint8, refID uint64) {
	// Get or create referendum in database
	var ref types.Ref
	err := m.db.FirstOrCreate(&ref, types.Ref{
		NetworkID: networkID,
		RefID:     refID,
	}).Error

	if err == nil {
		// If newly created, set default values
		if ref.Title == "" {
			ref.Title = fmt.Sprintf("Referendum #%d", refID)
			ref.Status = "Unknown"
			ref.Submitter = "Unknown"
			m.db.Save(&ref)
		}

		// Update thread mapping
		m.threadMapping[threadID] = &ThreadInfo{
			ThreadID:  threadID,
			NetworkID: networkID,
			RefID:     refID,
			RefDBID:   ref.ID,
		}
	}
}

func (m *Manager) GetThreadInfo(threadID string) *ThreadInfo {
	return m.threadMapping[threadID]
}

func (m *Manager) SyncThreads(session *discordgo.Session, guildID string) error {
	log.Println("Starting thread synchronization...")

	// Get all active threads
	threads, err := session.GuildThreadsActive(guildID)
	if err != nil {
		return fmt.Errorf("get active threads: %w", err)
	}

	synced := 0
	for _, thread := range threads.Threads {
		// Check if thread belongs to a referendum channel
		network := m.networks.FindByChannelID(thread.ParentID)
		if network != nil {
			refID := m.ExtractRefID(thread.Name)
			if refID > 0 {
				m.UpdateThreadMapping(thread.ID, network.ID, refID)
				synced++
				log.Printf("Synced thread: %s -> %s Ref #%d", thread.Name, network.Name, refID)
			}
		}
	}

	// Also check archived threads
	for _, network := range m.networks.GetAll() {
		if network.DiscordChannelID == "" {
			continue
		}

		publicThreads, err := session.ThreadsArchived(network.DiscordChannelID, nil, 100)
		if err == nil {
			for _, thread := range publicThreads.Threads {
				refID := m.ExtractRefID(thread.Name)
				if refID > 0 {
					m.UpdateThreadMapping(thread.ID, network.ID, refID)
					synced++
					log.Printf("Synced archived thread: %s -> %s Ref #%d", thread.Name, network.Name, refID)
				}
			}
		}
	}

	log.Printf("Thread synchronization complete. Synced %d threads", synced)
	return nil
}

func (m *Manager) ExtractRefID(name string) uint64 {
	patterns := []string{
		`^#?(\d+)\s*[:|-]`,
		`^#?(\d+)\s+`,
		`\[(\d+)\]`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(name)
		if len(matches) > 1 {
			if id, err := strconv.ParseUint(matches[1], 10, 64); err == nil {
				return id
			}
		}
	}
	return 0
}

func (m *Manager) GetOrCreateRef(networkID uint8, refNum uint64, creator string, isAdmin bool) (*types.Ref, error) {
	var ref types.Ref
	err := m.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&ref, "network_id = ? AND ref_id = ?", networkID, refNum).Error; err != nil {
			if err == gorm.ErrRecordNotFound && isAdmin {
				net := m.networks.GetByID(networkID)
				if net == nil {
					return fmt.Errorf("network not found")
				}

				ref = types.Ref{
					NetworkID: networkID,
					RefID:     refNum,
					Submitter: creator,
					Status:    "Unknown",
					Title:     fmt.Sprintf("%s Referendum #%d", net.Name, refNum),
				}
				if err := tx.Create(&ref).Error; err != nil {
					return err
				}
			} else {
				return err
			}
		}
		return nil
	})

	return &ref, err
}

func (m *Manager) FindThread(session *discordgo.Session, guildID, channelID string, refNum int) (*discordgo.Channel, error) {
	// Get active threads
	threads, err := session.GuildThreadsActive(guildID)
	if err != nil {
		return nil, err
	}

	// Pattern matching
	patterns := []string{
		fmt.Sprintf(`^#?%d\s*[:|-]`, refNum),
		fmt.Sprintf(`^#?%d\s+`, refNum),
		fmt.Sprintf(`\[%d\]`, refNum),
	}

	// Check active threads
	for _, thread := range threads.Threads {
		if thread.ParentID == channelID {
			for _, pattern := range patterns {
				if matched, _ := regexp.MatchString(pattern, thread.Name); matched {
					return thread, nil
				}
			}
		}
	}

	// Check archived threads
	return m.findArchivedThread(session, channelID, patterns)
}

func (m *Manager) findArchivedThread(session *discordgo.Session, channelID string, patterns []string) (*discordgo.Channel, error) {
	publicThreads, err := session.ThreadsArchived(channelID, nil, 100)
	if err == nil {
		for _, thread := range publicThreads.Threads {
			for _, pattern := range patterns {
				if matched, _ := regexp.MatchString(pattern, thread.Name); matched {
					// Unarchive
					archived := false
					_, err := session.ChannelEdit(thread.ID, &discordgo.ChannelEdit{
						Archived: &archived,
					})
					if err == nil {
						return thread, nil
					}
				}
			}
		}
	}

	return nil, fmt.Errorf("thread not found")
}
