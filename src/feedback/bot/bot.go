package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"github.com/redis/go-redis/v9"
	"github.com/stake-plus/govcomms/src/feedback/data"
	sharedconfig "github.com/stake-plus/govcomms/src/shared/config"
	shareddata "github.com/stake-plus/govcomms/src/shared/data"
	shareddiscord "github.com/stake-plus/govcomms/src/shared/discord"
	sharedgov "github.com/stake-plus/govcomms/src/shared/gov"
	sharedpolkassembly "github.com/stake-plus/govcomms/src/shared/polkassembly"
	"gorm.io/gorm"
)

type Bot struct {
	config         *sharedconfig.FeedbackConfig
	db             *gorm.DB
	redis          *redis.Client
	session        *discordgo.Session
	networkManager *sharedgov.NetworkManager
	refManager     *sharedgov.ReferendumManager
	polkassembly   *sharedpolkassembly.Service
	runtimeCtx     context.Context
	cancelFunc     context.CancelFunc
}

const feedbackEmbedColor = 0x5865F2

func (b *Bot) ensureNetworkManager() error {
	if b.networkManager != nil {
		return nil
	}

	mgr, err := sharedgov.NewNetworkManager(b.db)
	if err != nil {
		return err
	}

	b.networkManager = mgr
	return nil
}

func New(cfg *sharedconfig.FeedbackConfig, db *gorm.DB, rdb *redis.Client) (*Bot, error) {
	session, err := discordgo.New("Bot " + cfg.Base.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Discord session: %w", err)
	}

	session.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildMessageReactions |
		discordgo.IntentsMessageContent |
		discordgo.IntentsDirectMessages

	networkManager, err := sharedgov.NewNetworkManager(db)
	if err != nil {
		log.Printf("feedback: failed to create network manager: %v", err)
		networkManager = nil
	}

	refManager := sharedgov.NewReferendumManager(db)

	var paService *sharedpolkassembly.Service
	if networkManager != nil {
		if service, svcErr := sharedpolkassembly.NewService(
			sharedpolkassembly.ServiceConfig{
				Endpoint: cfg.PolkassemblyEndpoint,
				Logger:   log.Default(),
			},
			networkManager.GetAll(),
		); svcErr != nil {
			log.Printf("feedback: polkassembly service disabled: %v", svcErr)
		} else {
			paService = service
		}
	}

	bot := &Bot{
		config:         cfg,
		db:             db,
		redis:          rdb,
		session:        session,
		networkManager: networkManager,
		refManager:     refManager,
		polkassembly:   paService,
	}

	bot.initHandlers()
	return bot, nil
}

func (b *Bot) initHandlers() {
	b.session.AddHandler(b.onReady)
	b.session.AddHandler(b.onInteractionCreate)
	b.session.AddHandler(b.onThreadCreate)
	b.session.AddHandler(b.onThreadUpdate)
}

func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	log.Printf("Feedback bot logged in as: %v#%v", s.State.User.Username, s.State.User.Discriminator)

	if err := shareddiscord.RegisterSlashCommands(s, b.config.Base.GuildID, shareddiscord.CommandFeedback); err != nil {
		log.Printf("feedback: failed to register slash commands: %v", err)
	} else {
		log.Printf("feedback: slash command registered")
	}

	if b.runtimeCtx != nil {
		go func(ctx context.Context) {
			if err := b.syncActiveThreads(ctx); err != nil {
				log.Printf("feedback: initial thread sync failed: %v", err)
			}
		}(b.runtimeCtx)
	}
}

func (b *Bot) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	if i.ApplicationCommandData().Name != shareddiscord.CommandFeedback {
		return
	}

	b.handleFeedbackSlash(s, i)
}

func (b *Bot) onThreadCreate(s *discordgo.Session, t *discordgo.ThreadCreate) {
	if t.Channel != nil {
		if err := b.processThread(t.Channel); err != nil {
			log.Printf("feedback: thread create mapping failed: %v", err)
		}
	}
}

func (b *Bot) onThreadUpdate(s *discordgo.Session, t *discordgo.ThreadUpdate) {
	if t.Channel != nil {
		if err := b.processThread(t.Channel); err != nil {
			log.Printf("feedback: thread update mapping failed: %v", err)
		}
	}
}

func (b *Bot) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	b.cancelFunc = cancel
	b.runtimeCtx = ctx

	if err := b.session.Open(); err != nil {
		return fmt.Errorf("failed to open Discord connection: %w", err)
	}

	go func() {
		log.Println("feedback: starting network indexer service")
		data.IndexerService(ctx, b.db, 5*time.Minute, b.config.IndexerWorkers)
	}()

	go b.startReferendumSync(ctx)
	go b.startPolkassemblyMonitor(ctx)

	return nil
}

func (b *Bot) Stop() {
	if b.cancelFunc != nil {
		b.cancelFunc()
	}

	b.runtimeCtx = nil

	if b.session != nil {
		b.session.Close()
	}

	if b.db != nil {
		sqlDB, err := b.db.DB()
		if err == nil {
			sqlDB.Close()
		}
	}

	if b.redis != nil {
		b.redis.Close()
	}
}

func (b *Bot) handleFeedbackSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := i.Member
	if user == nil {
		log.Printf("feedback: interaction missing member context")
		return
	}

	message := ""
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "message" {
			message = strings.TrimSpace(opt.StringValue())
			break
		}
	}

	length := utf8.RuneCountInString(message)
	if length < 10 || length > 5000 {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Feedback must be between 10 and 5000 characters.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if b.config.FeedbackRoleID != "" && !shareddiscord.HasRole(s, b.config.Base.GuildID, user.User.ID, b.config.FeedbackRoleID) {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "You don't have permission to use this command.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource}); err != nil {
		log.Printf("feedback: failed to acknowledge interaction: %v", err)
		return
	}

	threadInfo, err := b.ensureThreadMapping(i.ChannelID)
	if err != nil {
		msg := "This command must be used in a referendum thread."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	network := b.networkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		msg := "Unable to identify the associated network for this thread."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	var ref sharedgov.Ref
	if err := b.db.First(&ref, threadInfo.RefDBID).Error; err != nil {
		log.Printf("feedback: failed to load referendum %d: %v", threadInfo.RefDBID, err)
		msg := "Could not load referendum details. Please try again later."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	authorTag := fmt.Sprintf("%s#%s", user.User.Username, user.User.Discriminator)

	msgRecord, err := data.SaveFeedbackMessage(b.db, &ref, authorTag, message)
	if err != nil {
		log.Printf("feedback: failed to persist message: %v", err)
		msg := "Failed to store feedback. Please try again later."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	if b.polkassembly != nil {
		if count, err := data.CountFeedbackMessages(b.db, threadInfo.RefDBID); err != nil {
			log.Printf("feedback: failed to count messages: %v", err)
		} else if count == 1 {
			gcURL := shareddata.GetSetting("gc_url")
			if gcURL == "" {
				gcURL = "http://localhost:3000"
			}
			gcURL = strings.TrimRight(gcURL, "/")
			link := fmt.Sprintf("%s/%s/%d", gcURL, strings.ToLower(network.Name), ref.RefID)

			networkName := network.Name
			refID := ref.RefID
			messageCopy := message
			messageID := msgRecord.ID

			go func() {
				commentID, err := b.polkassembly.PostFirstMessage(networkName, int(refID), messageCopy, link)
				if err != nil {
					log.Printf("feedback: polkassembly post failed: %v", err)
					return
				}
				if commentID != 0 {
					if err := data.UpdateFeedbackMessagePolkassembly(b.db, messageID, fmt.Sprintf("%d", commentID), nil, ""); err != nil {
						log.Printf("feedback: failed to update message with polkassembly id: %v", err)
					}
				}
			}()
		}
	}

	if b.redis != nil {
		payload := map[string]interface{}{
			"type":             "feedback_submitted",
			"network":          network.Name,
			"network_id":       network.ID,
			"ref_id":           ref.RefID,
			"discord_user_id":  user.User.ID,
			"discord_user_tag": authorTag,
			"message":          message,
			"timestamp":        time.Now().UTC().Format(time.RFC3339),
		}
		if err := data.PublishMessage(context.Background(), b.redis, payload); err != nil {
			log.Printf("feedback: failed to publish redis payload: %v", err)
		}
	}

	b.postFeedbackMessage(s, i.ChannelID, network, &ref, authorTag, message)

	response := fmt.Sprintf("✅ Thank you %s! Your feedback for %s referendum #%d has been posted.",
		authorTag, network.Name, ref.RefID)
	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &response})
}

func (b *Bot) postFeedbackMessage(s *discordgo.Session, threadID string, network *sharedgov.Network, ref *sharedgov.Ref, authorTag, message string) {
	if network == nil || ref == nil {
		return
	}

	const maxEmbedDescriptionLen = 4000
	desc := message
	if utf8.RuneCountInString(desc) > maxEmbedDescriptionLen {
		runes := []rune(desc)
		desc = string(runes[:maxEmbedDescriptionLen-1]) + "…"
	}

	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("Feedback for %s Referendum #%d", network.Name, ref.RefID),
		Description: desc,
		Color:       feedbackEmbedColor,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Submitted by %s", authorTag),
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	messageSend := &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{embed}}

	if utf8.RuneCountInString(message) > maxEmbedDescriptionLen {
		messageSend.Files = []*discordgo.File{
			{
				Name:   fmt.Sprintf("feedback-%d.txt", ref.RefID),
				Reader: strings.NewReader(message),
			},
		}
		if embed.Footer != nil {
			embed.Footer.Text += " • Full text attached"
		}
	}

	if _, err := s.ChannelMessageSendComplex(threadID, messageSend); err != nil {
		log.Printf("feedback: failed to post feedback message: %v", err)
	}
}

func (b *Bot) ensureThreadMapping(channelID string) (*sharedgov.ThreadInfo, error) {
	info, err := b.refManager.FindThread(channelID)
	if err == nil {
		return info, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	thread, fetchErr := b.session.Channel(channelID)
	if fetchErr != nil {
		return nil, fetchErr
	}

	if err := b.processThread(thread); err != nil {
		return nil, err
	}

	return b.refManager.FindThread(channelID)
}

func (b *Bot) processThread(thread *discordgo.Channel) error {
	if thread == nil {
		return fmt.Errorf("nil thread provided")
	}

	if err := b.ensureNetworkManager(); err != nil {
		return fmt.Errorf("network manager not initialized: %w", err)
	}

	network := b.networkManager.FindByChannelID(thread.ParentID)
	if network == nil {
		return fmt.Errorf("no network configured for channel %s", thread.ParentID)
	}

	refID, err := sharedgov.ParseRefIDFromTitle(thread.Name)
	if err != nil {
		return fmt.Errorf("failed to parse referendum id from thread %s: %w", thread.ID, err)
	}

	if err := b.refManager.UpsertThreadMapping(network.ID, refID, thread.ID); err != nil {
		return fmt.Errorf("failed to upsert thread mapping: %w", err)
	}

	return nil
}

func (b *Bot) startReferendumSync(ctx context.Context) {
	interval := time.Duration(b.config.IndexerIntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = time.Hour
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := b.syncActiveThreads(ctx); err != nil {
			log.Printf("feedback: referendum sync failed: %v", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *Bot) syncActiveThreads(ctx context.Context) error {
	if b.config.Base.GuildID == "" {
		return fmt.Errorf("guild id not configured")
	}

	threads, err := b.session.GuildThreadsActive(b.config.Base.GuildID)
	if err != nil {
		return err
	}

	for _, thread := range threads.Threads {
		if err := b.processThread(thread); err != nil {
			log.Printf("feedback: failed to sync thread %s: %v", thread.ID, err)
		}
	}

	return nil
}

func (b *Bot) startPolkassemblyMonitor(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	for {
		if err := b.refreshPolkassemblyCheck(); err != nil {
			log.Printf("feedback: polkassembly monitor error: %v", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *Bot) refreshPolkassemblyCheck() error {
	const batchSize = 50
	cutoff := time.Now().Add(-12 * time.Hour)

	var refs []sharedgov.Ref
	if err := b.db.Where("last_reply_check IS NULL OR last_reply_check < ?", cutoff).
		Limit(batchSize).Find(&refs).Error; err != nil {
		return err
	}

	now := time.Now()
	for _, ref := range refs {
		if err := b.db.Model(&sharedgov.Ref{}).
			Where("id = ?", ref.ID).
			Update("last_reply_check", now).Error; err != nil {
			log.Printf("feedback: failed to update last_reply_check for ref %d: %v", ref.ID, err)
		}
	}

	return nil
}
