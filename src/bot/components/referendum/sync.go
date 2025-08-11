package referendum

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/bot/config"
	"github.com/stake-plus/govcomms/src/bot/types"
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
	for _, net := range networks {
		networkMap[net.Name] = net.ID
	}

	for _, thread := range threads.Threads {
		if thread.Type != discordgo.ChannelTypeGuildPublicThread {
			continue
		}

		networkID, refID, err := parseThreadTitle(thread.Name)
		if err != nil {
			continue
		}

		var ref types.Ref
		if err := db.Where("network_id = ? AND ref_id = ?", networkID, refID).First(&ref).Error; err != nil {
			log.Printf("Referendum not found for thread %s", thread.Name)
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
				log.Printf("Synced thread: %s -> %s ref #%d", thread.ID, networks[networkID-1].Name, refID)
			}
		}
	}
}

func parseThreadTitle(title string) (networkID uint8, refID uint32, err error) {
	polkadotPattern := regexp.MustCompile(`(?i)(?:polkadot|dot)\s*#?\s*(\d+)`)
	kusamaPattern := regexp.MustCompile(`(?i)(?:kusama|ksm)\s*#?\s*(\d+)`)

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
