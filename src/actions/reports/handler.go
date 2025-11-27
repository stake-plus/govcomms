package reports

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	aicore "github.com/stake-plus/govcomms/src/ai/core"
	cache "github.com/stake-plus/govcomms/src/cache"
	sharedconfig "github.com/stake-plus/govcomms/src/config"
	shareddiscord "github.com/stake-plus/govcomms/src/discord"
	"github.com/stake-plus/govcomms/src/mcp"
	sharedgov "github.com/stake-plus/govcomms/src/polkadot-go/governance"
	"github.com/stake-plus/govcomms/src/reports"
	"gorm.io/gorm"
)

// Handler manages PDF report generation
type Handler struct {
	Config         *sharedconfig.ReportsConfig
	DB             *gorm.DB
	NetworkManager *sharedgov.NetworkManager
	RefManager     *sharedgov.ReferendumManager
	cacheManager   *cache.Manager
}

// NewHandler creates a new reports handler
func NewHandler(cfg *sharedconfig.ReportsConfig, db *gorm.DB, networkManager *sharedgov.NetworkManager, refManager *sharedgov.ReferendumManager) *Handler {
	cacheManager, err := cache.NewManager(cfg.TempDir)
	if err != nil {
		log.Printf("reports: failed to create cache manager: %v", err)
	}

	return &Handler{
		Config:         cfg,
		DB:             db,
		NetworkManager: networkManager,
		RefManager:     refManager,
		cacheManager:   cacheManager,
	}
}

// GenerateReport generates a PDF report for a referendum
func (h *Handler) GenerateReport(s *discordgo.Session, channelID string, network string, refID uint32, refDBID uint64) {
	log.Printf("reports: GenerateReport called for %s #%d", network, refID)

	if h.cacheManager == nil {
		log.Printf("reports: cache manager is nil, cannot generate report")
		return
	}

	// Get cache entry
	entry, err := h.cacheManager.EnsureEntry(network, refID)
	if err != nil {
		log.Printf("reports: failed to get cache entry: %v", err)
		return
	}

	// Check if summary exists (refresh must be complete)
	if entry.Summary == nil {
		log.Printf("reports: summary not ready yet for %s #%d", network, refID)
		return
	}

	log.Printf("reports: summary found, starting PDF generation for %s #%d", network, refID)
	// Generate PDF in background
	go h.generateAndSendPDF(s, channelID, network, refID, refDBID, entry)
}

// generateAndSendPDF generates a comprehensive PDF report and uploads it to Discord
func (h *Handler) generateAndSendPDF(s *discordgo.Session, channelID string, network string, refID uint32, refDBID uint64, entry *cache.Entry) {
	// Get channel name from Discord
	channelName := ""
	if channel, err := s.Channel(channelID); err == nil && channel != nil {
		channelName = channel.Name
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	log.Printf("reports: starting PDF report generation for %s #%d", network, refID)

	// Get proposal content
	proposalContent, err := h.cacheManager.GetProposalContent(network, refID)
	if err != nil {
		log.Printf("reports: failed to get proposal content: %v", err)
		return
	}

	// Get referendum from database
	var ref sharedgov.Ref
	if err := h.DB.Where("id = ?", refDBID).First(&ref).Error; err != nil {
		log.Printf("reports: failed to get referendum from DB: %v", err)
	}

	// Create AI client for additional analysis
	aiCfg := sharedconfig.LoadQAConfig(h.DB).AIConfig
	factoryCfg := aiCfg.FactoryConfig()
	aiClient, err := aicore.NewClient(factoryCfg)
	if err != nil {
		log.Printf("reports: failed to create AI client: %v", err)
		return
	}

	analyzer, err := reports.NewAnalyzer(aiClient)
	if err != nil {
		log.Printf("reports: failed to create report analyzer: %v", err)
		return
	}

	// Create MCP tool if MCP is enabled
	var mcpTool *aicore.Tool
	mcpCfg := sharedconfig.LoadMCPConfig(h.DB)
	if mcpCfg.Enabled {
		mcpTool = mcp.NewReferendaTool(mcpCfg.Listen, mcpCfg.AuthToken)
	}

	// Generate additional analysis sections in parallel
	type analysisResult struct {
		financials      *reports.FinancialAnalysis
		risks           *reports.RiskAnalysis
		timeline        *reports.TimelineAnalysis
		governance      *reports.GovernanceAnalysis
		positive        *reports.PositiveAnalysis
		steelMan        *reports.SteelManAnalysis
		recommendations *reports.Recommendations
		err             error
	}
	resultCh := make(chan analysisResult, 7)

	// Financials
	go func() {
		var result analysisResult
		defer func() { resultCh <- result }()
		result.financials, result.err = analyzer.AnalyzeFinancials(ctx, network, refID, mcpTool, entry.Summary)
	}()

	// Risks
	go func() {
		var result analysisResult
		defer func() { resultCh <- result }()
		result.risks, result.err = analyzer.AnalyzeRisks(ctx, network, refID, mcpTool, entry.Summary, entry.Claims, entry.TeamMembers)
	}()

	// Timeline
	go func() {
		var result analysisResult
		defer func() { resultCh <- result }()
		result.timeline, result.err = analyzer.AnalyzeTimeline(ctx, network, refID, mcpTool)
	}()

	// Governance
	go func() {
		var result analysisResult
		defer func() { resultCh <- result }()
		result.governance, result.err = analyzer.AnalyzeGovernance(ctx, network, refID, mcpTool)
	}()

	// Positive
	go func() {
		var result analysisResult
		defer func() { resultCh <- result }()
		result.positive, result.err = analyzer.AnalyzePositive(ctx, network, refID, mcpTool, entry.Summary)
	}()

	// Steel Man
	go func() {
		var result analysisResult
		defer func() { resultCh <- result }()
		result.steelMan, result.err = analyzer.AnalyzeSteelMan(ctx, network, refID, mcpTool, entry.Summary, entry.Claims, entry.TeamMembers)
	}()

	// Collect results
	var finalResult analysisResult
	for i := 0; i < 6; i++ {
		select {
		case r := <-resultCh:
			if r.financials != nil {
				finalResult.financials = r.financials
			}
			if r.risks != nil {
				finalResult.risks = r.risks
			}
			if r.timeline != nil {
				finalResult.timeline = r.timeline
			}
			if r.governance != nil {
				finalResult.governance = r.governance
			}
			if r.positive != nil {
				finalResult.positive = r.positive
			}
			if r.steelMan != nil {
				finalResult.steelMan = r.steelMan
			}
			if r.err != nil {
				log.Printf("reports: analysis error: %v", r.err)
			}
		case <-ctx.Done():
			log.Printf("reports: PDF analysis timeout")
			return
		}
	}

	// Generate recommendations
	if finalResult.positive != nil && finalResult.steelMan != nil {
		finalResult.recommendations, _ = analyzer.GenerateRecommendations(ctx, network, refID, mcpTool, entry.Summary,
			finalResult.financials, finalResult.risks, finalResult.positive, finalResult.steelMan)
		// Enhance recommendations with idea quality, team capability, AI vote
		if finalResult.recommendations != nil {
			analyzer.EnhanceRecommendations(ctx, finalResult.recommendations, network, refID, mcpTool, entry.TeamMembers, finalResult.positive, finalResult.steelMan)
		}
	}

	// Generate enhanced content
	enhancedContent, _ := analyzer.GenerateEnhancedContent(ctx, network, refID, mcpTool, entry.Summary, entry.TeamMembers, finalResult.financials)

	// Generate section notes (green/red boxes)
	backgroundNotes, _ := analyzer.GenerateSectionNotes(ctx, "Background Context",
		func() string {
			if enhancedContent != nil {
				return enhancedContent.BackgroundContext
			}
			if entry.Summary != nil {
				return entry.Summary.BackgroundContext
			}
			return ""
		}(), finalResult.positive, finalResult.steelMan)

	summaryNotes, _ := analyzer.GenerateSectionNotes(ctx, "Referenda Summary",
		func() string {
			if enhancedContent != nil {
				return enhancedContent.ReferendaSummary
			}
			if entry.Summary != nil {
				return entry.Summary.Summary
			}
			return ""
		}(), finalResult.positive, finalResult.steelMan)

	financialsNotes, _ := analyzer.GenerateSectionNotes(ctx, "Project Financials",
		func() string {
			if enhancedContent != nil {
				return enhancedContent.FinancialsDetail
			}
			return ""
		}(), finalResult.positive, finalResult.steelMan)

	// Generate enhanced team member details
	teamDetailsMap := make(map[string]*reports.TeamMemberDetails)
	if entry.TeamMembers != nil {
		for _, member := range entry.TeamMembers.Members {
			details, err := analyzer.GenerateTeamMemberDetails(ctx, member, network, refID, mcpTool)
			if err == nil && details != nil {
				teamDetailsMap[member.Name] = details
			}
		}
	}

	// Create report data
	reportData := &reports.ReportData{
		Network:              network,
		RefID:                refID,
		Title:                entry.Summary.Title,
		ChannelName:          channelName,
		Summary:              entry.Summary,
		Claims:               entry.Claims,
		TeamMembers:          entry.TeamMembers,
		ProposalText:         proposalContent,
		Ref:                  &ref,
		Financials:           finalResult.financials,
		RiskAssessment:       finalResult.risks,
		Timeline:             finalResult.timeline,
		Governance:           finalResult.governance,
		Positive:             finalResult.positive,
		SteelManning:         finalResult.steelMan,
		Recommendations:      finalResult.recommendations,
		EnhancedContent:      enhancedContent,
		BackgroundNotes:      backgroundNotes,
		SummaryNotes:         summaryNotes,
		FinancialsNotes:      financialsNotes,
		TeamMemberDetailsMap: teamDetailsMap,
	}

	// Generate PDF
	generator := reports.NewGenerator(h.Config.TempDir)
	pdfPath, err := generator.GeneratePDF(reportData)
	if err != nil {
		log.Printf("reports: PDF generation failed: %v", err)
		if _, err := shareddiscord.SendMessageNoEmbed(s, channelID, "⚠️ PDF report generation failed."); err != nil {
			log.Printf("reports: failed to send PDF error: %v", err)
		}
		return
	}

	// Upload PDF to Discord
	pdfFile, err := os.Open(pdfPath)
	if err != nil {
		log.Printf("reports: failed to open PDF file: %v", err)
		return
	}
	defer pdfFile.Close()
	defer os.Remove(pdfPath) // Clean up temp file

	fileInfo, err := pdfFile.Stat()
	if err != nil {
		log.Printf("reports: failed to stat PDF file: %v", err)
		return
	}

	// Discord has a 25MB file limit, check if we're under
	if fileInfo.Size() > 25*1024*1024 {
		log.Printf("reports: PDF file too large (%d bytes)", fileInfo.Size())
		if _, err := shareddiscord.SendMessageNoEmbed(s, channelID, "⚠️ PDF report is too large to upload."); err != nil {
			log.Printf("reports: failed to send PDF error: %v", err)
		}
		return
	}

	msg := &discordgo.MessageSend{
		Content: fmt.Sprintf("📄 **Referenda Reeeeeeeeeports**\n\nComprehensive PDF report for %s referendum #%d", network, refID),
		Files: []*discordgo.File{
			{
				Name:   fmt.Sprintf("referendum-%s-%d.pdf", strings.ToLower(network), refID),
				Reader: pdfFile,
			},
		},
	}

	if _, err := shareddiscord.SendComplexMessageNoEmbed(s, channelID, msg); err != nil {
		log.Printf("reports: failed to upload PDF: %v", err)
		if _, err := shareddiscord.SendMessageNoEmbed(s, channelID, "⚠️ Failed to upload PDF report."); err != nil {
			log.Printf("reports: failed to send PDF error: %v", err)
		}
		return
	}

	log.Printf("reports: PDF report successfully generated and uploaded for %s #%d", network, refID)
}

// HandleReportSlash handles the /report slash command
func (h *Handler) HandleReportSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Respond immediately
	if err := shareddiscord.InteractionRespondNoEmbed(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		log.Printf("reports: slash ack failed: %v", err)
		return
	}

	// Find thread info
	threadInfo, err := h.RefManager.FindThread(i.ChannelID)
	if err != nil || threadInfo == nil {
		formatted := shareddiscord.FormatStyledBlock("Report", "This command must be used in a referendum thread.")
		shareddiscord.InteractionResponseEditNoEmbed(s, i.Interaction, &discordgo.WebhookEdit{
			Content: &formatted,
		})
		return
	}

	network := h.NetworkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		formatted := shareddiscord.FormatStyledBlock("Report", "Failed to identify network.")
		shareddiscord.InteractionResponseEditNoEmbed(s, i.Interaction, &discordgo.WebhookEdit{
			Content: &formatted,
		})
		return
	}

	if h.cacheManager == nil {
		formatted := shareddiscord.FormatStyledBlock("Report", "Cache manager not initialized. Please check server configuration.")
		shareddiscord.InteractionResponseEditNoEmbed(s, i.Interaction, &discordgo.WebhookEdit{
			Content: &formatted,
		})
		log.Printf("reports: cache manager is nil in HandleReportSlash")
		return
	}

	// Check if summary exists
	entry, err := h.cacheManager.EnsureEntry(network.Name, uint32(threadInfo.RefID))
	if err != nil {
		formatted := shareddiscord.FormatStyledBlock("Report", fmt.Sprintf("Failed to get cache entry: %v", err))
		shareddiscord.InteractionResponseEditNoEmbed(s, i.Interaction, &discordgo.WebhookEdit{
			Content: &formatted,
		})
		return
	}

	if entry.Summary == nil {
		formatted := shareddiscord.FormatStyledBlock("Report", "No report has been generated yet. Run /refresh to download the most recent data and generate a report.")
		shareddiscord.InteractionResponseEditNoEmbed(s, i.Interaction, &discordgo.WebhookEdit{
			Content: &formatted,
		})
		return
	}

	// Generate and send PDF
	go h.generateAndSendPDF(s, i.ChannelID, network.Name, uint32(threadInfo.RefID), threadInfo.RefDBID, entry)

	// Send initial response
	formatted := shareddiscord.FormatStyledBlock("Report", fmt.Sprintf("Generating PDF report for %s referendum #%d...", network.Name, threadInfo.RefID))
	shareddiscord.InteractionResponseEditNoEmbed(s, i.Interaction, &discordgo.WebhookEdit{
		Content: &formatted,
	})
}
