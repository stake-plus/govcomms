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

type Manager struct {
	db       *gorm.DB
	networks *network.Manager
}

func NewManager(db *gorm.DB, networks *network.Manager) *Manager {
	return &Manager{
		db:       db,
		networks: networks,
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
	}
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
