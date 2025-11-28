package feedback

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/actions/core"
	"github.com/stake-plus/govcomms/src/actions/feedback/data"
	sharedconfig "github.com/stake-plus/govcomms/src/config"
	shareddata "github.com/stake-plus/govcomms/src/data"
	shareddiscord "github.com/stake-plus/govcomms/src/discord"
	sharedgov "github.com/stake-plus/govcomms/src/polkadot-go/governance"
	sharedpolkassembly "github.com/stake-plus/govcomms/src/polkadot-go/polkassembly"
	"gorm.io/gorm"
)

var _ core.Module = (*Module)(nil)

type Module struct {
	config         *sharedconfig.FeedbackConfig
	db             *gorm.DB
	session        *discordgo.Session
	handler        *Handler
	networkManager *sharedgov.NetworkManager
	refManager     *sharedgov.ReferendumManager
	polkassembly   *sharedpolkassembly.Service
	runtimeCtx     context.Context
	cancel         context.CancelFunc
}

const polkassemblyReplyColor = 0xF39C12

func (b *Module) ensureNetworkManager() error {
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

func NewModule(cfg *sharedconfig.FeedbackConfig, db *gorm.DB) (*Module, error) {
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
		return nil, fmt.Errorf("feedback: load networks: %w", err)
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

	module := &Module{
		config:         cfg,
		db:             db,
		session:        session,
		networkManager: networkManager,
		refManager:     refManager,
		polkassembly:   paService,
	}

	module.handler = &Handler{
		Config:         cfg,
		DB:             db,
		NetworkManager: networkManager,
		RefManager:     refManager,
		Deps: Dependencies{
			EnsureThreadMapping:     module.ensureThreadMapping,
			PostFeedbackMessage:     module.postFeedbackMessage,
			PostPolkassemblyMessage: module.postPolkassemblyMessage,
		},
	}

	module.initHandlers()
	return module, nil
}

// Name implements actions.Module.
func (b *Module) Name() string { return "feedback" }

func (b *Module) initHandlers() {
	b.session.AddHandler(b.onReady)
	b.session.AddHandler(b.onInteractionCreate)
	b.session.AddHandler(b.onThreadCreate)
	b.session.AddHandler(b.onThreadUpdate)
}

func (b *Module) onReady(s *discordgo.Session, r *discordgo.Ready) {
	username := formatDiscordUsername(s.State.User.Username, s.State.User.Discriminator)
	log.Printf("Feedback bot logged in as: %v", username)

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

func (b *Module) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	if i.ApplicationCommandData().Name != shareddiscord.CommandFeedback {
		return
	}

	b.handler.HandleSlash(s, i)
}

func (b *Module) onThreadCreate(s *discordgo.Session, t *discordgo.ThreadCreate) {
	if t.Channel != nil {
		if err := b.processThread(t.Channel); err != nil {
			log.Printf("feedback: thread create mapping failed: %v", err)
		}
	}
}

func (b *Module) onThreadUpdate(s *discordgo.Session, t *discordgo.ThreadUpdate) {
	if t.Channel != nil {
		if err := b.processThread(t.Channel); err != nil {
			log.Printf("feedback: thread update mapping failed: %v", err)
		}
	}
}

func (b *Module) Start(ctx context.Context) error {
	runtimeCtx, cancel := context.WithCancel(ctx)
	b.cancel = cancel
	b.runtimeCtx = runtimeCtx

	if err := b.session.Open(); err != nil {
		return fmt.Errorf("failed to open Discord connection: %w", err)
	}

	go func() {
		interval := time.Duration(b.config.IndexerIntervalMinutes) * time.Minute
		if interval <= 0 {
			interval = 5 * time.Minute
		}
		log.Printf("feedback: starting network indexer service (interval=%v)", interval)
		data.IndexerService(runtimeCtx, b.db, interval, b.config.IndexerWorkers)
	}()

	go b.startReferendumSync(runtimeCtx)
	go b.startPolkassemblyMonitor(runtimeCtx)

	return nil
}

func (b *Module) Stop(ctx context.Context) {
	if b.cancel != nil {
		b.cancel()
	}

	b.runtimeCtx = nil

	if b.session != nil {
		b.session.Close()
	}
}

func (b *Module) postFeedbackMessage(s *discordgo.Session, threadID string, network *sharedgov.Network, ref *sharedgov.Ref, authorTag, message string) {
	if network == nil || ref == nil {
		return
	}
	title := fmt.Sprintf("Feedback • %s #%d", network.Name, ref.RefID)
	body := fmt.Sprintf("%s\n\nSubmitted by %s\n🕒 %s UTC",
		strings.TrimSpace(message),
		authorTag,
		time.Now().UTC().Format(time.RFC822),
	)

	payloads := shareddiscord.BuildStyledMessages(title, body, "")
	if len(payloads) == 0 {
		return
	}

	for _, payload := range payloads {
		msg := &discordgo.MessageSend{
			Content: payload.Content,
		}
		if len(payload.Components) > 0 {
			msg.Components = payload.Components
		}
		if _, err := shareddiscord.SendComplexMessageNoEmbed(s, threadID, msg); err != nil {
			log.Printf("feedback: failed to post feedback message: %v", err)
			return
		}
	}
}

func (b *Module) ensureThreadMapping(channelID string) (*sharedgov.ThreadInfo, error) {
	info, err := b.refManager.FindThread(channelID)
	if err == nil {
		return info, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	if b.session == nil {
		return nil, fmt.Errorf("discord session not initialized")
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

func (b *Module) processThread(thread *discordgo.Channel) error {
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

func (b *Module) startReferendumSync(ctx context.Context) {
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

func (b *Module) syncActiveThreads(ctx context.Context) error {
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

func (b *Module) startPolkassemblyMonitor(ctx context.Context) {
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

func (b *Module) refreshPolkassemblyCheck() error {
	if b.polkassembly == nil {
		return nil
	}

	const batchSize = 50
	cutoff := time.Now().Add(-12 * time.Hour)

	var refs []sharedgov.Ref
	if err := b.db.Where("last_reply_check IS NULL OR last_reply_check < ?", cutoff).
		Limit(batchSize).Find(&refs).Error; err != nil {
		return err
	}

	for _, ref := range refs {
		if err := b.processPolkassemblyReplies(&ref); err != nil {
			log.Printf("feedback: polkassembly reply sync failed for ref %d: %v", ref.RefID, err)
			continue
		}

		checkTime := time.Now()
		if err := b.db.Model(&sharedgov.Ref{}).
			Where("id = ?", ref.ID).
			Update("last_reply_check", checkTime).Error; err != nil {
			log.Printf("feedback: failed to update last_reply_check for ref %d: %v", ref.ID, err)
		}
	}

	return nil
}

func (b *Module) processPolkassemblyReplies(ref *sharedgov.Ref) error {
	if b.polkassembly == nil {
		return nil
	}
	if err := b.ensureNetworkManager(); err != nil {
		return err
	}

	network := b.networkManager.GetByID(ref.NetworkID)
	if network == nil {
		return fmt.Errorf("no network configured for id %d", ref.NetworkID)
	}

	messages, err := data.GetPolkassemblyMessages(b.db, ref.ID)
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		return nil
	}

	knownIDs := make(map[string]struct{}, len(messages))
	parentCommentIDs := make(map[int]struct{}, len(messages))
	for _, msg := range messages {
		if msg.PolkassemblyCommentID == nil || *msg.PolkassemblyCommentID == "" {
			continue
		}
		id := *msg.PolkassemblyCommentID
		knownIDs[id] = struct{}{}
		if parsed, err := strconv.Atoi(id); err == nil {
			parentCommentIDs[parsed] = struct{}{}
		}
	}

	if len(parentCommentIDs) == 0 {
		return nil
	}

	comments, err := b.polkassembly.ListComments(network.Name, int(ref.RefID))
	if err != nil {
		return err
	}

	threadInfo, err := b.refManager.GetThreadInfo(network.ID, uint32(ref.RefID))
	if err != nil {
		return fmt.Errorf("thread mapping not found for network %d ref %d", network.ID, ref.RefID)
	}

	for _, comment := range comments {
		if comment.ParentID == nil {
			continue
		}
		if _, ok := parentCommentIDs[*comment.ParentID]; !ok {
			continue
		}

		idStr := strconv.Itoa(comment.ID)
		if _, exists := knownIDs[idStr]; exists {
			continue
		}

		var userID *int
		if comment.User.ID > 0 {
			uid := comment.User.ID
			userID = &uid
		}

		createdAt := comment.ParsedCreatedAt()
		_, err := data.SaveExternalPolkassemblyReply(
			b.db,
			ref.ID,
			comment.User.Username,
			comment.Content,
			userID,
			comment.User.Username,
			idStr,
			createdAt,
		)
		if err != nil {
			log.Printf("feedback: failed to store polkassembly reply %d: %v", comment.ID, err)
			continue
		}

		knownIDs[idStr] = struct{}{}
		parentCommentIDs[comment.ID] = struct{}{}

		b.announcePolkassemblyReply(threadInfo.ThreadID, network, ref, comment)

	}

	return nil
}

func (b *Module) announcePolkassemblyReply(threadID string, network *sharedgov.Network, ref *sharedgov.Ref, comment sharedpolkassembly.Comment) {
	if b.session == nil || threadID == "" {
		return
	}

	content := strings.TrimSpace(comment.Content)
	if content == "" {
		content = "_(no content)_"
	}
	content = shareddiscord.WrapURLsNoEmbed(content)

	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("Polkassembly reply from %s", comment.User.Username),
		Description: content,
		Color:       polkassemblyReplyColor,
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Polkassembly",
		},
	}

	if ts := comment.ParsedCreatedAt(); !ts.IsZero() {
		embed.Timestamp = ts.UTC().Format(time.RFC3339)
	} else {
		embed.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	embed.URL = fmt.Sprintf("https://%s.polkassembly.io/referendum/%d?commentId=%d",
		strings.ToLower(network.Name), ref.RefID, comment.ID)

	if _, err := shareddiscord.SendComplexMessageNoEmbed(b.session, threadID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
	}); err != nil {
		log.Printf("feedback: failed to post polkassembly reply to Discord: %v", err)
	}
}

// postPolkassemblyMessage posts a message to Polkassembly immediately and returns the comment ID
func (b *Module) postPolkassemblyMessage(network *sharedgov.Network, ref *sharedgov.Ref, message string) (string, error) {
	if b.polkassembly == nil {
		return "", fmt.Errorf("polkassembly service is not configured")
	}
	if network == nil {
		return "", fmt.Errorf("network is nil")
	}
	if ref == nil {
		return "", fmt.Errorf("ref is nil")
	}

	log.Printf("feedback: posting message to polkassembly for %s ref #%d", network.Name, ref.RefID)

	gcURL := shareddata.GetSetting("gc_url")
	if gcURL == "" {
		gcURL = "http://localhost:3000"
	}
	gcURL = strings.TrimRight(gcURL, "/")
	link := fmt.Sprintf("%s/%s/%d", gcURL, strings.ToLower(network.Name), ref.RefID)

	commentID, err := b.polkassembly.PostFirstMessage(network.Name, int(ref.RefID), message, link)
	if err != nil {
		log.Printf("feedback: PostFirstMessage failed: %v", err)
		return "", fmt.Errorf("post to polkassembly failed: %w", err)
	}

	if commentID == "" {
		return "", fmt.Errorf("post succeeded but no comment ID returned")
	}

	log.Printf("feedback: successfully posted to polkassembly (comment ID: %s) for %s ref #%d", commentID, network.Name, ref.RefID)
	return commentID, nil
}
