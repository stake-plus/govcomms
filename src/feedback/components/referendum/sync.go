package referendum

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/feedback/config"
	"github.com/stake-plus/govcomms/src/feedback/types"
	"gorm.io/gorm"
)

func StartPeriodicSync(ctx context.Context, session *discordgo.Session, db *gorm.DB, config *config.Config, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	runSync(session, db, config.GuildID)

	for {
		select {
		case <-ctx.Done():
			log.Println("Stopping referendum sync")
			return
		case <-ticker.C:
			log.Println("Running periodic thread sync")
			runSync(session, db, config.GuildID)
		}
	}
}

func runSync(session *discordgo.Session, db *gorm.DB, guildID string) {
	log.Printf("Starting thread sync for guild %s", guildID)

	guild, err := session.Guild(guildID)
	if err != nil {
		log.Printf("Failed to get guild: %v", err)
		return
	}

	threads, err := session.GuildThreadsActive(guild.ID)
	if err != nil {
		log.Printf("Failed to get active threads: %v", err)
		return
	}

	var networks []types.Network
	if err := db.Find(&networks).Error; err != nil {
		log.Printf("Failed to load networks: %v", err)
		return
	}

	networkMap := make(map[string]uint8)
	channelNetworkMap := make(map[string]uint8)
	for _, net := range networks {
		networkMap[net.Name] = net.ID
		if net.DiscordChannelID != "" {
			channelNetworkMap[net.DiscordChannelID] = net.ID
		}
	}

	log.Printf("Found %d active threads to check", len(threads.Threads))

	for _, thread := range threads.Threads {
		if thread.Type != discordgo.ChannelTypeGuildPublicThread {
			continue
		}

		// Determine network from parent channel
		networkID, exists := channelNetworkMap[thread.ParentID]
		if !exists {
			continue
		}

		// Extract referendum ID from title
		refID, err := parseRefIDFromTitle(thread.Name)
		if err != nil {
			continue
		}

		log.Printf("Thread '%s' (ID: %s) parsed as network %d ref %d", thread.Name, thread.ID, networkID, refID)

		var ref types.Ref
		if err := db.Where("network_id = ? AND ref_id = ?", networkID, refID).First(&ref).Error; err != nil {
			log.Printf("Referendum not found in DB for thread %s (network %d ref %d)", thread.Name, networkID, refID)
			continue
		}

		var refThread types.RefThread
		err = db.Where("thread_id = ?", thread.ID).First(&refThread).Error
		if err == gorm.ErrRecordNotFound {
			refThread = types.RefThread{
				ThreadID:  thread.ID,
				RefDBID:   ref.ID,
				NetworkID: networkID,
				RefID:     uint64(refID),
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}

			if err := db.Create(&refThread).Error; err != nil {
				log.Printf("Failed to create thread mapping: %v", err)
			} else {
				networkName := "Unknown"
				for _, net := range networks {
					if net.ID == networkID {
						networkName = net.Name
						break
					}
				}
				log.Printf("Created thread mapping: %s -> %s ref #%d (DB ID: %d)", thread.ID, networkName, refID, ref.ID)
			}
		} else if err == nil {
			if err := db.Model(&refThread).Update("updated_at", time.Now()).Error; err != nil {
				log.Printf("Failed to update thread mapping: %v", err)
			}
		} else {
			log.Printf("Database error checking thread: %v", err)
		}
	}

	log.Printf("Thread sync completed")
}

func parseRefIDFromTitle(title string) (uint32, error) {
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
				return 0, fmt.Errorf("no referendum number found")
			}
		}
	}

	refNumStr := matches[1]
	refNum, err := strconv.ParseUint(refNumStr, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid referendum number: %s", refNumStr)
	}

	return uint32(refNum), nil
}
