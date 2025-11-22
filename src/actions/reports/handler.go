package reports

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/reports"
	aicore "github.com/stake-plus/govcomms/src/ai/core"
	cache "github.com/stake-plus/govcomms/src/cache"
	sharedconfig "github.com/stake-plus/govcomms/src/config"
	shareddiscord "github.com/stake-plus/govcomms/src/discord"
	sharedgov "github.com/stake-plus/govcomms/src/polkadot-go/governance"
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

	// Generate PDF in background
	go h.generateAndSendPDF(s, channelID, network, refID, refDBID, entry)
}

// generateAndSendPDF generates a comprehensive PDF report and uploads it to Discord
func (h *Handler) generateAndSendPDF(s *discordgo.Session, channelID string, network string, refID uint32, refDBID uint64, entry *cache.Entry) {
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
		result.financials, result.err = analyzer.AnalyzeFinancials(ctx, proposalContent, entry.Summary)
	}()

	// Risks
	go func() {
		var result analysisResult
		defer func() { resultCh <- result }()
		result.risks, result.err = analyzer.AnalyzeRisks(ctx, proposalContent, entry.Summary, entry.Claims, entry.TeamMembers)
	}()

	// Timeline
	go func() {
		var result analysisResult
		defer func() { resultCh <- result }()
		result.timeline, result.err = analyzer.AnalyzeTimeline(ctx, proposalContent)
	}()

	// Governance
	go func() {
		var result analysisResult
		defer func() { resultCh <- result }()
		result.governance, result.err = analyzer.AnalyzeGovernance(ctx, proposalContent, network)
	}()

	// Positive
	go func() {
		var result analysisResult
		defer func() { resultCh <- result }()
		result.positive, result.err = analyzer.AnalyzePositive(ctx, proposalContent, entry.Summary)
	}()

	// Steel Man
	go func() {
		var result analysisResult
		defer func() { resultCh <- result }()
		result.steelMan, result.err = analyzer.AnalyzeSteelMan(ctx, proposalContent, entry.Summary, entry.Claims, entry.TeamMembers)
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
		finalResult.recommendations, _ = analyzer.GenerateRecommendations(ctx, proposalContent, entry.Summary,
			finalResult.financials, finalResult.risks, finalResult.positive, finalResult.steelMan)
	}

	// Create report data
	reportData := &reports.ReportData{
		Network:         network,
		RefID:           refID,
		Title:           entry.Summary.Title,
		Summary:         entry.Summary,
		Claims:          entry.Claims,
		TeamMembers:     entry.TeamMembers,
		ProposalText:    proposalContent,
		Ref:             &ref,
		Financials:      finalResult.financials,
		RiskAssessment:  finalResult.risks,
		Timeline:        finalResult.timeline,
		Governance:      finalResult.governance,
		Positive:        finalResult.positive,
		SteelManning:    finalResult.steelMan,
		Recommendations: finalResult.recommendations,
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

