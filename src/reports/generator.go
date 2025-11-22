package reports

import (
	"fmt"
	"log"
	"strings"
	"time"
	"unicode"

	"github.com/jung-kurt/gofpdf/v2"
	"github.com/stake-plus/govcomms/src/actions/research/components/claims"
	"github.com/stake-plus/govcomms/src/cache"
	sharedgov "github.com/stake-plus/govcomms/src/polkadot-go/governance"
)

// sanitizeTextForPDF converts UTF-8 special characters to ASCII equivalents
// to avoid encoding issues in gofpdf
func sanitizeTextForPDF(text string) string {
	if text == "" {
		return text
	}

	var result strings.Builder
	result.Grow(len(text))

	for _, r := range text {
		switch r {
		// Common UTF-8 characters that cause issues
		case '\u2013': // en dash
			result.WriteString("-")
		case '\u2014': // em dash
			result.WriteString("--")
		case '\u2018': // left single quotation mark
			result.WriteString("'")
		case '\u2019': // right single quotation mark
			result.WriteString("'")
		case '\u201C': // left double quotation mark
			result.WriteString("\"")
		case '\u201D': // right double quotation mark
			result.WriteString("\"")
		case '\u2026': // horizontal ellipsis
			result.WriteString("...")
		case '\u00A0': // non-breaking space
			result.WriteString(" ")
		case '\u00AD': // soft hyphen
			result.WriteString("-")
		case '\u200B': // zero-width space
			// Skip zero-width characters
			continue
		case '\u200C': // zero-width non-joiner
			continue
		case '\u200D': // zero-width joiner
			continue
		case '\uFEFF': // zero-width no-break space
			continue
		default:
			// Keep printable ASCII and basic Latin characters
			if r < 128 || unicode.IsPrint(r) {
				result.WriteRune(r)
			} else if unicode.IsSpace(r) {
				result.WriteString(" ")
			} else {
				// Replace other non-ASCII characters with a safe fallback
				result.WriteString("?")
			}
		}
	}

	return result.String()
}

// Helper functions to sanitize text before adding to PDF
func (g *Generator) cellFormat(pdf *gofpdf.Fpdf, w, h float64, txt, borderStr string, ln int, alignStr string, fill bool, link int, linkStr string) {
	pdf.CellFormat(w, h, sanitizeTextForPDF(txt), borderStr, ln, alignStr, fill, link, linkStr)
}

func (g *Generator) multiCell(pdf *gofpdf.Fpdf, w, h float64, txt, borderStr, alignStr string, fill bool) {
	pdf.MultiCell(w, h, sanitizeTextForPDF(txt), borderStr, alignStr, fill)
}

// drawStatusIcon draws a colored status icon box and returns the x position after the icon
func (g *Generator) drawStatusIcon(pdf *gofpdf.Fpdf, x, y float64, status string) float64 {
	iconSize := 8.0
	var fillColor [3]int
	var borderColor [3]int
	var textColor [3]int
	var iconText string

	switch status {
	case "verified", "valid", "ok":
		fillColor = [3]int{220, 255, 220}
		borderColor = [3]int{0, 150, 0}
		textColor = [3]int{0, 150, 0}
		iconText = "OK"
	case "not_verified", "invalid", "rejected", "x":
		fillColor = [3]int{255, 220, 220}
		borderColor = [3]int{200, 0, 0}
		textColor = [3]int{200, 0, 0}
		iconText = "X"
	case "warning", "unverified", "!":
		fillColor = [3]int{255, 255, 220}
		borderColor = [3]int{200, 150, 0}
		textColor = [3]int{200, 150, 0}
		iconText = "!"
	default: // unknown
		fillColor = [3]int{240, 240, 240}
		borderColor = [3]int{128, 128, 128}
		textColor = [3]int{128, 128, 128}
		iconText = "?"
	}

	pdf.SetFillColor(fillColor[0], fillColor[1], fillColor[2])
	pdf.Rect(x, y, iconSize, iconSize, "F")
	pdf.SetDrawColor(borderColor[0], borderColor[1], borderColor[2])
	pdf.Rect(x, y, iconSize, iconSize, "D")
	pdf.SetTextColor(textColor[0], textColor[1], textColor[2])
	pdf.SetFont("Arial", "B", 8)
	pdf.SetXY(x+2, y+1)
	pdf.CellFormat(4, 6, iconText, "", 0, "C", false, 0, "")
	pdf.SetTextColor(0, 0, 0)

	return x + iconSize + 3
}

// drawColoredBox draws a colored box with text
func (g *Generator) drawColoredBox(pdf *gofpdf.Fpdf, x, y, w, h float64, red, green, blue int, title, content string) {
	// Save current position
	currentY := pdf.GetY()

	// Draw box background
	pdf.SetFillColor(red, green, blue)
	pdf.Rect(x, y, w, h, "F")

	// Draw border
	pdf.SetDrawColor(0, 0, 0)
	pdf.SetLineWidth(0.5)
	pdf.Rect(x, y, w, h, "D")

	// Add title
	if title != "" {
		pdf.SetXY(x+3, y+3)
		pdf.SetFont("Arial", "B", 10)
		pdf.SetTextColor(0, 0, 0)
		g.cellFormat(pdf, w-6, 5, title, "", 0, "L", false, 0, "")
	}

	// Add content
	if content != "" {
		pdf.SetXY(x+3, y+8)
		pdf.SetFont("Arial", "", 9)
		pdf.SetTextColor(0, 0, 0)
		g.multiCell(pdf, w-6, 4, content, "", "", false)
	}

	// Restore position
	pdf.SetXY(x, currentY)
}

// drawGreenBox draws a green box for positive notes and returns the height used
// Checks if box fits on current page, moves to next page if needed
func (g *Generator) drawGreenBox(pdf *gofpdf.Fpdf, x, y, w float64, title string, items []string) float64 {
	if len(items) == 0 {
		return 0
	}

	// Sanitize each item first, then join
	sanitizedItems := make([]string, len(items))
	for i, item := range items {
		sanitizedItems[i] = sanitizeTextForPDF(item)
	}

	// Use "-" instead of bullet to avoid encoding issues
	content := strings.Join(sanitizedItems, "\n- ")
	if content != "" {
		content = "- " + content
	}

	// Calculate height needed for content by measuring text
	pdf.SetFont("Arial", "B", 10)
	titleHeight := 8.0

	pdf.SetFont("Arial", "", 9)
	// Measure content height - calculate by processing each item separately to avoid SplitText issues
	contentWidth := w - 6.0
	totalLines := 0

	// Process each item separately to calculate lines more safely
	for _, item := range sanitizedItems {
		itemText := "- " + item
		// Use a safer method to calculate lines - split into chunks if needed
		if len(itemText) > 500 {
			// For very long items, estimate lines based on character count
			// Estimate: roughly 50-60 characters per line at 9pt font
			charsPerLine := 55
			estimatedLines := (len(itemText) / charsPerLine) + 1
			if estimatedLines < 1 {
				estimatedLines = 1
			}
			totalLines += estimatedLines
		} else {
			// For shorter items, use SplitText safely with error recovery
			func() {
				defer func() {
					if r := recover(); r != nil {
						// If SplitText panics, estimate lines
						charsPerLine := 55
						estimatedLines := (len(itemText) / charsPerLine) + 1
						if estimatedLines < 1 {
							estimatedLines = 1
						}
						totalLines += estimatedLines
					}
				}()
				lines := pdf.SplitText(itemText, contentWidth)
				totalLines += len(lines)
			}()
		}
	}

	contentHeight := float64(totalLines) * 4.5

	totalHeight := titleHeight + contentHeight + 10.0 // Add padding

	// Check if box fits on current page (with 20mm margin at bottom)
	pageHeight := 297.0 // A4 height in mm
	bottomMargin := 20.0
	currentY := y
	if currentY+totalHeight > pageHeight-bottomMargin {
		// Box won't fit, move to next page
		pdf.AddPage()
		currentY = 20.0 // Top margin
	}

	g.drawColoredBox(pdf, x, currentY, w, totalHeight, 220, 255, 220, title, content)
	return totalHeight
}

// drawRedBox draws a red box for concerns and returns the height used
// Checks if box fits on current page, moves to next page if needed
func (g *Generator) drawRedBox(pdf *gofpdf.Fpdf, x, y, w float64, title string, items []string) float64 {
	if len(items) == 0 {
		return 0
	}

	// Sanitize each item first, then join
	sanitizedItems := make([]string, len(items))
	for i, item := range items {
		sanitizedItems[i] = sanitizeTextForPDF(item)
	}

	// Use "-" instead of bullet to avoid encoding issues
	content := strings.Join(sanitizedItems, "\n- ")
	if content != "" {
		content = "- " + content
	}

	// Calculate height needed for content by measuring text
	pdf.SetFont("Arial", "B", 10)
	titleHeight := 8.0

	pdf.SetFont("Arial", "", 9)
	// Measure content height - calculate by processing each item separately to avoid SplitText issues
	contentWidth := w - 6.0
	totalLines := 0

	// Process each item separately to calculate lines more safely
	for _, item := range sanitizedItems {
		itemText := "- " + item
		// Use a safer method to calculate lines - split into chunks if needed
		if len(itemText) > 500 {
			// For very long items, estimate lines based on character count
			// Estimate: roughly 50-60 characters per line at 9pt font
			charsPerLine := 55
			estimatedLines := (len(itemText) / charsPerLine) + 1
			if estimatedLines < 1 {
				estimatedLines = 1
			}
			totalLines += estimatedLines
		} else {
			// For shorter items, use SplitText safely with error recovery
			func() {
				defer func() {
					if r := recover(); r != nil {
						// If SplitText panics, estimate lines
						charsPerLine := 55
						estimatedLines := (len(itemText) / charsPerLine) + 1
						if estimatedLines < 1 {
							estimatedLines = 1
						}
						totalLines += estimatedLines
					}
				}()
				lines := pdf.SplitText(itemText, contentWidth)
				totalLines += len(lines)
			}()
		}
	}

	contentHeight := float64(totalLines) * 4.5

	totalHeight := titleHeight + contentHeight + 10.0 // Add padding

	// Check if box fits on current page (with 20mm margin at bottom)
	pageHeight := 297.0 // A4 height in mm
	bottomMargin := 20.0
	currentY := y
	if currentY+totalHeight > pageHeight-bottomMargin {
		// Box won't fit, move to next page
		pdf.AddPage()
		currentY = 20.0 // Top margin
	}

	g.drawColoredBox(pdf, x, currentY, w, totalHeight, 255, 220, 220, title, content)
	return totalHeight
}

// Generator creates PDF reports for referendums
type Generator struct {
	tempDir string
}

// NewGenerator creates a new PDF report generator
func NewGenerator(tempDir string) *Generator {
	return &Generator{
		tempDir: tempDir,
	}
}

// ReportData contains all data needed to generate a report
type ReportData struct {
	Network      string
	RefID        uint32
	Title        string
	Summary      *cache.SummaryData
	Claims       *cache.ClaimsData
	TeamMembers  *cache.TeamsData
	ProposalText string
	Ref          *sharedgov.Ref
	// Additional analysis sections
	Financials      *FinancialAnalysis
	RiskAssessment  *RiskAnalysis
	Timeline        *TimelineAnalysis
	Governance      *GovernanceAnalysis
	Positive        *PositiveAnalysis
	SteelManning    *SteelManAnalysis
	Recommendations *Recommendations
	// Enhanced content
	EnhancedContent      *EnhancedContent
	BackgroundNotes      *SectionNotes
	SummaryNotes         *SectionNotes
	FinancialsNotes      *SectionNotes
	TeamMemberDetailsMap map[string]*TeamMemberDetails // Keyed by team member name
}

// FinancialAnalysis contains financial breakdown
type FinancialAnalysis struct {
	TotalAmount string
	Breakdown   []BudgetItem
	Milestones  []Milestone
	ROI         string
	Concerns    []string
	GeneratedAt time.Time
}

type BudgetItem struct {
	Category string
	Amount   string
	Purpose  string
}

type Milestone struct {
	Name        string
	Amount      string
	Deliverable string
	Timeline    string
}

// RiskAnalysis contains risk assessment
type RiskAnalysis struct {
	TechnicalRisks []RiskItem
	FinancialRisks []RiskItem
	ExecutionRisks []RiskItem
	OverallRisk    string // Low/Medium/High
	Mitigation     []string
	GeneratedAt    time.Time
}

type RiskItem struct {
	Risk        string
	Severity    string // Low/Medium/High
	Likelihood  string // Low/Medium/High
	Description string
}

// TimelineAnalysis contains timeline feasibility
type TimelineAnalysis struct {
	ProposedTimeline string
	Feasibility      string // Realistic/Unrealistic/Ambitious
	Concerns         []string
	Recommendations  []string
	GeneratedAt      time.Time
}

// GovernanceAnalysis contains governance impact
type GovernanceAnalysis struct {
	Impact        string // Low/Medium/High
	Description   string
	NetworkEffect string
	Precedents    []string
	Concerns      []string
	GeneratedAt   time.Time
}

// PositiveAnalysis contains positive aspects
type PositiveAnalysis struct {
	Strengths        []string
	Opportunities    []string
	ValueProposition string
	Innovation       []string
	GeneratedAt      time.Time
}

// SteelManAnalysis contains steel manning (why it's bad)
type SteelManAnalysis struct {
	Concerns     []string
	Weaknesses   []string
	RedFlags     []string
	Alternatives []string
	GeneratedAt  time.Time
}

// Recommendations contains final recommendations
type Recommendations struct {
	Verdict     string // Approve/Deny/Modify
	Confidence  string // High/Medium/Low
	Reasoning   string
	Conditions  []string // If modifying
	KeyPoints   []string
	GeneratedAt time.Time
	// Enhanced verdict fields
	IdeaQuality    string // Good/Bad/Uncertain
	TeamCapability string // Can deliver/Cannot deliver/Uncertain
	AIVote         string // Aye/Nay/Abstain
}

// EnhancedContent contains expanded analysis sections
type EnhancedContent struct {
	BackgroundContext string // 2 paragraphs: people, idea, other context
	ReferendaSummary  string // 2 paragraphs: everything needed to vote
	FinancialsDetail  string // 2 paragraphs: current ask, future asks, side projects
	GeneratedAt       time.Time
}

// SectionNotes contains green/red box content for sections
type SectionNotes struct {
	Positive []string // Green box content
	Concerns []string // Red box content
}

// TeamMemberDetails contains enhanced team member information
type TeamMemberDetails struct {
	SocialHandles map[string][]string // All social handles
	Skills        []string
	WorkHistory   string
	Verified      []string // Verified/confirmed items
	Concerns      []string // Concerns/worries
}

// GeneratePDF creates a comprehensive PDF report
func (g *Generator) GeneratePDF(data *ReportData) (string, error) {
	if data == nil {
		return "", fmt.Errorf("report data is nil")
	}

	// Create PDF
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(15, 20, 15)
	pdf.SetAutoPageBreak(true, 20)
	pdf.SetHeaderFunc(func() {
		// Header with REEEEEEEEEEEEE DAO branding
		pdf.SetFont("Arial", "B", 16)
		pdf.SetTextColor(59, 130, 246) // Blue color
		pdf.CellFormat(0, 10, "Referenda Reeeeeeeeeports", "", 0, "C", false, 0, "")
		pdf.Ln(12)
	})

	pdf.SetFooterFunc(func() {
		pdf.SetY(-15)
		pdf.SetFont("Arial", "I", 8)
		pdf.SetTextColor(128, 128, 128)
		pdf.CellFormat(0, 10, fmt.Sprintf("Generated by REEEEEEEEEEEEE DAO - Page %d", pdf.PageNo()), "", 0, "C", false, 0, "")
	})

	// Page 1: Referendum Overview (only page that forces a new page)
	g.addOverviewPage(pdf, data)

	// Context & Summary (flows naturally)
	g.addSummaryPage(pdf, data)

	// Project Financials (flows naturally)
	g.addFinancialsPage(pdf, data)

	// Team Members (flows naturally)
	g.addTeamPages(pdf, data)

	// Claims Page (flows naturally)
	g.addClaimsPage(pdf, data)

	// Positive Analysis Page (flows naturally)
	g.addPositiveAnalysisPage(pdf, data)

	// Critical Analysis Page (flows naturally)
	g.addSteelManningPage(pdf, data)

	// Recommendations Page (flows naturally)
	g.addRecommendationsPage(pdf, data)

	// Save PDF
	filename := fmt.Sprintf("referendum-%s-%d-%s.pdf",
		strings.ToLower(data.Network),
		data.RefID,
		time.Now().Format("20060102-150405"))
	filepath := fmt.Sprintf("%s/%s", g.tempDir, filename)

	if err := pdf.OutputFileAndClose(filepath); err != nil {
		return "", fmt.Errorf("save PDF: %w", err)
	}

	log.Printf("reports: generated PDF report: %s", filepath)
	return filepath, nil
}

func (g *Generator) addOverviewPage(pdf *gofpdf.Fpdf, data *ReportData) {
	pdf.AddPage()

	// Title
	pdf.SetFont("Arial", "B", 20)
	pdf.SetTextColor(0, 0, 0)
	pdf.CellFormat(0, 15, "Referendum Overview", "", 0, "L", false, 0, "")
	pdf.Ln(20)

	// Network and ID
	pdf.SetFont("Arial", "B", 14)
	g.cellFormat(pdf, 0, 10, fmt.Sprintf("Network: %s", data.Network), "", 0, "L", false, 0, "")
	pdf.Ln(8)
	pdf.CellFormat(0, 10, fmt.Sprintf("Referendum #%d", data.RefID), "", 0, "L", false, 0, "")
	pdf.Ln(8)

	// Title
	pdf.SetFont("Arial", "B", 12)
	pdf.CellFormat(0, 10, "Title:", "", 0, "L", false, 0, "")
	pdf.Ln(6)
	pdf.SetFont("Arial", "", 11)
	g.multiCell(pdf, 0, 7, data.Title, "", "", false)
	pdf.Ln(10)

	// Refreshed date
	if data.Summary != nil {
		pdf.SetFont("Arial", "I", 10)
		pdf.SetTextColor(128, 128, 128)
		pdf.CellFormat(0, 8, fmt.Sprintf("Report Generated: %s",
			time.Now().Format("January 2, 2006 at 3:04 PM")), "", 0, "L", false, 0, "")
		pdf.Ln(10)
	}

	// Quick stats
	pdf.SetTextColor(0, 0, 0)
	pdf.SetFont("Arial", "B", 12)
	pdf.CellFormat(0, 10, "Quick Statistics", "", 0, "L", false, 0, "")
	pdf.Ln(8)
	pdf.SetFont("Arial", "", 10)

	if data.Claims != nil {
		pdf.CellFormat(0, 7, fmt.Sprintf("Total Claims Analyzed: %d", data.Claims.TotalClaims), "", 0, "L", false, 0, "")
		pdf.Ln(6)
	}

	if data.TeamMembers != nil {
		pdf.CellFormat(0, 7, fmt.Sprintf("Team Members: %d", len(data.TeamMembers.Members)), "", 0, "L", false, 0, "")
		pdf.Ln(6)
	}

	if data.Summary != nil {
		validCount := len(data.Summary.ValidClaims)
		invalidCount := len(data.Summary.InvalidClaims)
		unverifiedCount := len(data.Summary.UnverifiedClaims)
		pdf.CellFormat(0, 7, fmt.Sprintf("Claims Status: %d Valid, %d Invalid, %d Unverified",
			validCount, invalidCount, unverifiedCount), "", 0, "L", false, 0, "")
		pdf.Ln(6)
	}
}

func (g *Generator) addSummaryPage(pdf *gofpdf.Fpdf, data *ReportData) {
	// Don't force page break - let it flow naturally

	pdf.SetFont("Arial", "B", 16)
	pdf.CellFormat(0, 12, "Context & Summary", "", 0, "L", false, 0, "")
	pdf.Ln(15)

	// Background Context
	pdf.SetFont("Arial", "B", 12)
	pdf.SetTextColor(0, 0, 0)
	pdf.CellFormat(0, 10, "Background Context", "", 0, "L", false, 0, "")
	pdf.Ln(8)
	pdf.SetFont("Arial", "", 10)

	backgroundText := ""
	if data.EnhancedContent != nil && data.EnhancedContent.BackgroundContext != "" {
		backgroundText = data.EnhancedContent.BackgroundContext
	} else if data.Summary != nil {
		backgroundText = data.Summary.BackgroundContext
	}

	if backgroundText == "" {
		pdf.SetFont("Arial", "I", 11)
		pdf.SetTextColor(128, 128, 128)
		g.multiCell(pdf, 0, 7, "Background context not available.", "", "", false)
	} else {
		g.multiCell(pdf, 0, 6, backgroundText, "", "", false)
	}

	// Green/Red boxes for Background Context
	pdf.Ln(5)
	if data.BackgroundNotes != nil {
		boxWidth := 180.0
		boxX := 15.0

		if len(data.BackgroundNotes.Positive) > 0 {
			boxY := pdf.GetY()
			boxHeight := g.drawGreenBox(pdf, boxX, boxY, boxWidth, "Noteworthy Positive Aspects", data.BackgroundNotes.Positive)
			// Update Y position - box may have moved to new page
			if boxY+boxHeight > 277 { // If box was near bottom, it moved to new page
				pdf.SetY(20 + boxHeight + 5)
			} else {
				pdf.SetY(boxY + boxHeight + 5)
			}
		}

		if len(data.BackgroundNotes.Concerns) > 0 {
			boxY := pdf.GetY()
			boxHeight := g.drawRedBox(pdf, boxX, boxY, boxWidth, "Noteworthy Concerns", data.BackgroundNotes.Concerns)
			// Update Y position - box may have moved to new page
			if boxY+boxHeight > 277 { // If box was near bottom, it moved to new page
				pdf.SetY(20 + boxHeight + 5)
			} else {
				pdf.SetY(boxY + boxHeight + 5)
			}
		}
	}
	pdf.Ln(10)

	// Referenda Summary
	pdf.SetFont("Arial", "B", 12)
	pdf.CellFormat(0, 10, "Referenda Summary", "", 0, "L", false, 0, "")
	pdf.Ln(8)
	pdf.SetFont("Arial", "", 10)

	summaryText := ""
	if data.EnhancedContent != nil && data.EnhancedContent.ReferendaSummary != "" {
		summaryText = data.EnhancedContent.ReferendaSummary
	} else if data.Summary != nil {
		summaryText = data.Summary.Summary
	}

	if summaryText == "" {
		pdf.SetFont("Arial", "I", 11)
		pdf.SetTextColor(128, 128, 128)
		g.multiCell(pdf, 0, 7, "Summary not available.", "", "", false)
	} else {
		g.multiCell(pdf, 0, 6, summaryText, "", "", false)
	}

	// Green/Red boxes for Summary
	pdf.Ln(5)
	if data.SummaryNotes != nil {
		boxWidth := 180.0
		boxX := 15.0

		if len(data.SummaryNotes.Positive) > 0 {
			boxY := pdf.GetY()
			boxHeight := g.drawGreenBox(pdf, boxX, boxY, boxWidth, "Noteworthy Positive Aspects", data.SummaryNotes.Positive)
			// Update Y position - box may have moved to new page
			if boxY+boxHeight > 277 { // If box was near bottom, it moved to new page
				pdf.SetY(20 + boxHeight + 5)
			} else {
				pdf.SetY(boxY + boxHeight + 5)
			}
		}

		if len(data.SummaryNotes.Concerns) > 0 {
			boxY := pdf.GetY()
			boxHeight := g.drawRedBox(pdf, boxX, boxY, boxWidth, "Noteworthy Concerns", data.SummaryNotes.Concerns)
			// Update Y position - box may have moved to new page
			if boxY+boxHeight > 277 { // If box was near bottom, it moved to new page
				pdf.SetY(20 + boxHeight + 5)
			} else {
				pdf.SetY(boxY + boxHeight + 5)
			}
		}
	}
}

func (g *Generator) addFinancialsPage(pdf *gofpdf.Fpdf, data *ReportData) {
	// Don't force page break - let it flow naturally

	pdf.SetFont("Arial", "B", 16)
	pdf.CellFormat(0, 12, "Project Financials", "", 0, "L", false, 0, "")
	pdf.Ln(15)

	// Enhanced Financials Detail
	pdf.SetFont("Arial", "B", 12)
	pdf.CellFormat(0, 10, "Financial Overview", "", 0, "L", false, 0, "")
	pdf.Ln(8)
	pdf.SetFont("Arial", "", 10)

	financialsText := ""
	if data.EnhancedContent != nil && data.EnhancedContent.FinancialsDetail != "" {
		financialsText = data.EnhancedContent.FinancialsDetail
	}

	if financialsText == "" {
		pdf.SetFont("Arial", "I", 11)
		pdf.SetTextColor(128, 128, 128)
		g.multiCell(pdf, 0, 7, "Financial details not available.", "", "", false)
	} else {
		g.multiCell(pdf, 0, 6, financialsText, "", "", false)
	}

	// Green/Red boxes for Financials
	pdf.Ln(5)
	if data.FinancialsNotes != nil {
		boxWidth := 180.0
		boxX := 15.0

		if len(data.FinancialsNotes.Positive) > 0 {
			boxY := pdf.GetY()
			boxHeight := g.drawGreenBox(pdf, boxX, boxY, boxWidth, "Noteworthy Positive Aspects", data.FinancialsNotes.Positive)
			// Update Y position - box may have moved to new page
			if boxY+boxHeight > 277 { // If box was near bottom, it moved to new page
				pdf.SetY(20 + boxHeight + 5)
			} else {
				pdf.SetY(boxY + boxHeight + 5)
			}
		}

		if len(data.FinancialsNotes.Concerns) > 0 {
			boxY := pdf.GetY()
			boxHeight := g.drawRedBox(pdf, boxX, boxY, boxWidth, "Noteworthy Concerns", data.FinancialsNotes.Concerns)
			// Update Y position - box may have moved to new page
			if boxY+boxHeight > 277 { // If box was near bottom, it moved to new page
				pdf.SetY(20 + boxHeight + 5)
			} else {
				pdf.SetY(boxY + boxHeight + 5)
			}
		}
	}
	pdf.Ln(10)

	if data.Financials == nil {
		return
	}

	// Total Amount
	pdf.SetFont("Arial", "B", 12)
	g.cellFormat(pdf, 0, 10, fmt.Sprintf("Total Requested: %s", data.Financials.TotalAmount), "", 0, "L", false, 0, "")
	pdf.Ln(12)

	// Budget Breakdown
	if len(data.Financials.Breakdown) > 0 {
		pdf.SetFont("Arial", "B", 11)
		pdf.CellFormat(0, 8, "Budget Breakdown", "", 0, "L", false, 0, "")
		pdf.Ln(8)
		pdf.SetFont("Arial", "", 9)
		for _, item := range data.Financials.Breakdown {
			g.cellFormat(pdf, 60, 7, item.Category, "", 0, "L", false, 0, "")
			g.cellFormat(pdf, 40, 7, item.Amount, "", 0, "R", false, 0, "")
			pdf.Ln(6)
			if item.Purpose != "" {
				pdf.SetFont("Arial", "I", 8)
				g.multiCell(pdf, 0, 5, item.Purpose, "", "", false)
				pdf.SetFont("Arial", "", 9)
				pdf.Ln(3)
			}
		}
		pdf.Ln(8)
	}

	// Milestones
	if len(data.Financials.Milestones) > 0 {
		pdf.SetFont("Arial", "B", 11)
		pdf.CellFormat(0, 8, "Payment Milestones", "", 0, "L", false, 0, "")
		pdf.Ln(8)
		pdf.SetFont("Arial", "", 9)
		for _, milestone := range data.Financials.Milestones {
			pdf.SetFont("Arial", "B", 9)
			g.cellFormat(pdf, 0, 7, milestone.Name, "", 0, "L", false, 0, "")
			pdf.Ln(5)
			pdf.SetFont("Arial", "", 9)
			g.cellFormat(pdf, 60, 6, fmt.Sprintf("Amount: %s", milestone.Amount), "", 0, "L", false, 0, "")
			g.cellFormat(pdf, 0, 6, fmt.Sprintf("Timeline: %s", milestone.Timeline), "", 0, "L", false, 0, "")
			pdf.Ln(5)
			if milestone.Deliverable != "" {
				g.multiCell(pdf, 0, 5, fmt.Sprintf("Deliverable: %s", milestone.Deliverable), "", "", false)
				pdf.Ln(3)
			}
		}
	}

	// ROI
	if data.Financials.ROI != "" {
		pdf.Ln(8)
		pdf.SetFont("Arial", "B", 11)
		pdf.CellFormat(0, 8, "Expected ROI/Value", "", 0, "L", false, 0, "")
		pdf.Ln(8)
		pdf.SetFont("Arial", "", 9)
		g.multiCell(pdf, 0, 6, data.Financials.ROI, "", "", false)
	}

	// Financial Concerns in red box
	if len(data.Financials.Concerns) > 0 {
		pdf.Ln(8)
		boxWidth := 180.0
		boxX := 15.0
		boxY := pdf.GetY()
		boxHeight := g.drawRedBox(pdf, boxX, boxY, boxWidth, "Financial Concerns", data.Financials.Concerns)
		// Update Y position - box may have moved to new page
		if boxY+boxHeight > 277 { // If box was near bottom, it moved to new page
			pdf.SetY(20 + boxHeight + 5)
		} else {
			pdf.SetY(boxY + boxHeight + 5)
		}
	}
}

func (g *Generator) addTeamPages(pdf *gofpdf.Fpdf, data *ReportData) {
	if data.TeamMembers == nil || len(data.TeamMembers.Members) == 0 {
		return
	}

	// Add team member index (no forced page break)
	pdf.SetFont("Arial", "B", 16)
	pdf.CellFormat(0, 12, "Team Member Index", "", 0, "L", false, 0, "")
	pdf.Ln(15)

	pdf.SetFont("Arial", "B", 11)
	pdf.CellFormat(80, 8, "Name / Moniker", "B", 0, "L", false, 0, "")
	pdf.CellFormat(30, 8, "Role in Project", "B", 0, "L", false, 0, "")
	pdf.Ln(10)

	pdf.SetFont("Arial", "", 10)
	for idx, member := range data.TeamMembers.Members {
		role := member.Role
		if role == "" {
			role = "Not specified"
		}
		g.cellFormat(pdf, 80, 7, fmt.Sprintf("%d. %s", idx+1, member.Name), "", 0, "L", false, 0, "")
		g.cellFormat(pdf, 30, 7, role, "", 0, "L", false, 0, "")
		pdf.Ln(7)
	}
	pdf.Ln(10)

	// Individual team member pages (no forced page breaks)
	for _, member := range data.TeamMembers.Members {
		// Add spacing between members but don't force new page
		if pdf.GetY() > 250 { // If near bottom of page, add new page
			pdf.AddPage()
		} else {
			pdf.Ln(10) // Add spacing
		}

		pdf.SetFont("Arial", "B", 16)
		pdf.CellFormat(0, 12, "Team Member Analysis", "", 0, "L", false, 0, "")
		pdf.Ln(15)

		// Name and Role
		pdf.SetFont("Arial", "B", 14)
		g.cellFormat(pdf, 0, 10, member.Name, "", 0, "L", false, 0, "")
		pdf.Ln(8)
		if member.Role != "" {
			pdf.SetFont("Arial", "", 11)
			g.cellFormat(pdf, 0, 8, fmt.Sprintf("Role: %s", member.Role), "", 0, "L", false, 0, "")
			pdf.Ln(10)
		}

		// Verification Status
		pdf.SetFont("Arial", "B", 11)
		pdf.CellFormat(0, 8, "Verification Status", "", 0, "L", false, 0, "")
		pdf.Ln(8)
		pdf.SetFont("Arial", "", 10)

		currentY := pdf.GetY()
		if member.IsReal != nil {
			if *member.IsReal {
				iconX := g.drawStatusIcon(pdf, 15, currentY, "verified")
				pdf.SetXY(iconX, currentY)
				g.cellFormat(pdf, 0, 7, "Verified Real Person", "", 0, "L", false, 0, "")
			} else {
				iconX := g.drawStatusIcon(pdf, 15, currentY, "not_verified")
				pdf.SetXY(iconX, currentY)
				g.cellFormat(pdf, 0, 7, "Not Verified", "", 0, "L", false, 0, "")
			}
		} else {
			iconX := g.drawStatusIcon(pdf, 15, currentY, "unknown")
			pdf.SetXY(iconX, currentY)
			g.cellFormat(pdf, 0, 7, "Unknown", "", 0, "L", false, 0, "")
		}
		pdf.Ln(6)

		currentY = pdf.GetY()
		if member.HasStatedSkills != nil {
			if *member.HasStatedSkills {
				iconX := g.drawStatusIcon(pdf, 15, currentY, "verified")
				pdf.SetXY(iconX, currentY)
				g.cellFormat(pdf, 0, 7, "Skills Verified", "", 0, "L", false, 0, "")
			} else {
				iconX := g.drawStatusIcon(pdf, 15, currentY, "warning")
				pdf.SetXY(iconX, currentY)
				g.cellFormat(pdf, 0, 7, "Skills Unverified", "", 0, "L", false, 0, "")
			}
		} else {
			iconX := g.drawStatusIcon(pdf, 15, currentY, "unknown")
			pdf.SetXY(iconX, currentY)
			g.cellFormat(pdf, 0, 7, "Unknown", "", 0, "L", false, 0, "")
		}
		pdf.Ln(10)
		pdf.SetTextColor(0, 0, 0)

		// Enhanced team member details
		details := data.TeamMemberDetailsMap[member.Name]

		// Social Handles
		pdf.SetFont("Arial", "B", 11)
		pdf.CellFormat(0, 8, "Social Handles & Contact Information", "", 0, "L", false, 0, "")
		pdf.Ln(8)
		pdf.SetFont("Arial", "", 9)

		if details != nil && details.SocialHandles != nil {
			handleLabels := map[string]string{
				"twitter":  "Twitter",
				"github":   "GitHub",
				"discord":  "Discord",
				"element":  "Element",
				"email":    "Email",
				"linkedin": "LinkedIn",
				"facebook": "Facebook",
				"forum":    "Forum",
				"youtube":  "YouTube",
				"other":    "Other",
			}

			for key, label := range handleLabels {
				if handles, ok := details.SocialHandles[key]; ok && len(handles) > 0 {
					g.cellFormat(pdf, 0, 6, fmt.Sprintf("%s: %s", label, strings.Join(handles, ", ")), "", 0, "L", false, 0, "")
					pdf.Ln(5)
				}
			}

			// Fallback to basic URLs if enhanced details not available
			if len(details.SocialHandles) == 0 {
				allURLs := []string{}
				allURLs = append(allURLs, member.GitHub...)
				allURLs = append(allURLs, member.Twitter...)
				allURLs = append(allURLs, member.LinkedIn...)
				allURLs = append(allURLs, member.Other...)
				allURLs = append(allURLs, member.VerifiedURLs...)

				if len(allURLs) > 0 {
					for _, url := range allURLs {
						g.cellFormat(pdf, 0, 6, url, "", 0, "L", false, 0, "")
						pdf.Ln(5)
					}
				}
			}
		} else {
			// Fallback to basic URLs
			allURLs := []string{}
			allURLs = append(allURLs, member.GitHub...)
			allURLs = append(allURLs, member.Twitter...)
			allURLs = append(allURLs, member.LinkedIn...)
			allURLs = append(allURLs, member.Other...)
			allURLs = append(allURLs, member.VerifiedURLs...)

			if len(allURLs) > 0 {
				for _, url := range allURLs {
					g.cellFormat(pdf, 0, 6, url, "", 0, "L", false, 0, "")
					pdf.Ln(5)
				}
			} else {
				pdf.SetFont("Arial", "I", 9)
				pdf.SetTextColor(128, 128, 128)
				g.cellFormat(pdf, 0, 6, "No profile links available", "", 0, "L", false, 0, "")
				pdf.SetTextColor(0, 0, 0)
			}
		}
		pdf.Ln(8)

		// Skills
		if details != nil && len(details.Skills) > 0 {
			pdf.SetFont("Arial", "B", 11)
			pdf.CellFormat(0, 8, "Known Skills", "", 0, "L", false, 0, "")
			pdf.Ln(8)
			pdf.SetFont("Arial", "", 9)
			for _, skill := range details.Skills {
				pdf.CellFormat(5, 6, "-", "", 0, "L", false, 0, "")
				g.multiCell(pdf, 0, 6, skill, "", "", false)
				pdf.Ln(2)
			}
			pdf.Ln(8)
		}

		// Work History
		if details != nil && details.WorkHistory != "" {
			pdf.SetFont("Arial", "B", 11)
			pdf.CellFormat(0, 8, "Work History & Background", "", 0, "L", false, 0, "")
			pdf.Ln(8)
			pdf.SetFont("Arial", "", 9)
			g.multiCell(pdf, 0, 6, details.WorkHistory, "", "", false)
			pdf.Ln(8)
		} else if member.Capability != "" {
			pdf.SetFont("Arial", "B", 11)
			pdf.CellFormat(0, 8, "Capability Assessment", "", 0, "L", false, 0, "")
			pdf.Ln(8)
			pdf.SetFont("Arial", "", 9)
			g.multiCell(pdf, 0, 6, member.Capability, "", "", false)
			pdf.Ln(8)
		}

		// Green/Red boxes for team member
		if details != nil {
			boxWidth := 180.0
			boxX := 15.0

			if len(details.Verified) > 0 {
				boxY := pdf.GetY()
				boxHeight := g.drawGreenBox(pdf, boxX, boxY, boxWidth, "Verified/Confirmed", details.Verified)
				// Update Y position - box may have moved to new page
				if boxY+boxHeight > 277 { // If box was near bottom, it moved to new page
					pdf.SetY(20 + boxHeight + 5)
				} else {
					pdf.SetY(boxY + boxHeight + 5)
				}
			}

			if len(details.Concerns) > 0 {
				boxY := pdf.GetY()
				boxHeight := g.drawRedBox(pdf, boxX, boxY, boxWidth, "Concerns/Worries", details.Concerns)
				// Update Y position - box may have moved to new page
				if boxY+boxHeight > 277 { // If box was near bottom, it moved to new page
					pdf.SetY(20 + boxHeight + 5)
				} else {
					pdf.SetY(boxY + boxHeight + 5)
				}
			}
		}
	}
}

func (g *Generator) addClaimsPage(pdf *gofpdf.Fpdf, data *ReportData) {
	// Don't force page break - let it flow naturally

	pdf.SetFont("Arial", "B", 16)
	pdf.CellFormat(0, 12, "Referendum Claims & Warranties", "", 0, "L", false, 0, "")
	pdf.Ln(15)

	if data.Claims == nil || len(data.Claims.Results) == 0 {
		pdf.SetFont("Arial", "I", 11)
		pdf.SetTextColor(128, 128, 128)
		g.multiCell(pdf, 0, 7, "No claims data available.", "", "", false)
		return
	}

	pdf.SetFont("Arial", "", 10)
	g.multiCell(pdf, 0, 6, "This section lists all claims made in the proposal, their verification status, and a description of what the AI did to verify each claim.", "", "", false)
	pdf.Ln(10)

	// Group by status
	valid := []cache.ClaimResult{}
	invalid := []cache.ClaimResult{}
	unknown := []cache.ClaimResult{}

	for _, claim := range data.Claims.Results {
		switch claim.Status {
		case string(claims.StatusValid):
			valid = append(valid, claim)
		case string(claims.StatusRejected):
			invalid = append(invalid, claim)
		case string(claims.StatusUnknown):
			unknown = append(unknown, claim)
		}
	}

	// Valid Claims
	if len(valid) > 0 {
		currentY := pdf.GetY()
		iconX := g.drawStatusIcon(pdf, 15, currentY, "valid")
		pdf.SetXY(iconX, currentY)
		pdf.SetFont("Arial", "B", 12)
		pdf.SetTextColor(0, 150, 0)
		g.cellFormat(pdf, 0, 10, fmt.Sprintf("Valid Claims (%d)", len(valid)), "", 0, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
		pdf.Ln(10)
		pdf.SetTextColor(0, 0, 0)
		pdf.SetFont("Arial", "", 9)
		for _, claim := range valid {
			pdf.CellFormat(5, 6, "-", "", 0, "L", false, 0, "")
			pdf.SetFont("Arial", "B", 9)
			g.multiCell(pdf, 0, 6, claim.Claim, "", "", false)
			pdf.SetFont("Arial", "", 9)
			pdf.Ln(2)
			pdf.SetFont("Arial", "", 8)
			pdf.SetTextColor(100, 100, 100)
			statusText := fmt.Sprintf("Status: [VERIFIED] - %s", claim.Status)
			if claim.Evidence != "" {
				statusText += fmt.Sprintf("\nVerification Process: %s", claim.Evidence)
			}
			if len(claim.SourceURLs) > 0 {
				statusText += fmt.Sprintf("\nSources: %s", strings.Join(claim.SourceURLs, ", "))
			}
			g.multiCell(pdf, 0, 4, statusText, "", "", false)
			pdf.SetTextColor(0, 0, 0)
			pdf.SetFont("Arial", "", 9)
			pdf.Ln(5)
		}
		pdf.Ln(8)
	}

	// Invalid Claims
	if len(invalid) > 0 {
		currentY := pdf.GetY()
		iconX := g.drawStatusIcon(pdf, 15, currentY, "invalid")
		pdf.SetXY(iconX, currentY)
		pdf.SetFont("Arial", "B", 12)
		pdf.SetTextColor(200, 0, 0)
		g.cellFormat(pdf, 0, 10, fmt.Sprintf("Invalid Claims (%d)", len(invalid)), "", 0, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
		pdf.Ln(10)
		pdf.SetTextColor(0, 0, 0)
		pdf.SetFont("Arial", "", 9)
		for _, claim := range invalid {
			pdf.CellFormat(5, 6, "-", "", 0, "L", false, 0, "")
			pdf.SetFont("Arial", "B", 9)
			g.multiCell(pdf, 0, 6, claim.Claim, "", "", false)
			pdf.SetFont("Arial", "", 9)
			pdf.Ln(2)
			pdf.SetFont("Arial", "", 8)
			pdf.SetTextColor(100, 100, 100)
			statusText := fmt.Sprintf("Status: [REJECTED] - %s", claim.Status)
			if claim.Evidence != "" {
				statusText += fmt.Sprintf("\nRejection Reason: %s", claim.Evidence)
			}
			if len(claim.SourceURLs) > 0 {
				statusText += fmt.Sprintf("\nSources Checked: %s", strings.Join(claim.SourceURLs, ", "))
			}
			g.multiCell(pdf, 0, 4, statusText, "", "", false)
			pdf.SetTextColor(0, 0, 0)
			pdf.SetFont("Arial", "", 9)
			pdf.Ln(5)
		}
		pdf.Ln(8)
	}

	// Unknown Claims
	if len(unknown) > 0 {
		currentY := pdf.GetY()
		iconX := g.drawStatusIcon(pdf, 15, currentY, "unverified")
		pdf.SetXY(iconX, currentY)
		pdf.SetFont("Arial", "B", 12)
		pdf.SetTextColor(150, 150, 0)
		g.cellFormat(pdf, 0, 10, fmt.Sprintf("Unverified Claims (%d)", len(unknown)), "", 0, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
		pdf.Ln(10)
		pdf.SetTextColor(0, 0, 0)
		pdf.SetFont("Arial", "", 9)
		for _, claim := range unknown {
			pdf.CellFormat(5, 6, "-", "", 0, "L", false, 0, "")
			pdf.SetFont("Arial", "B", 9)
			g.multiCell(pdf, 0, 6, claim.Claim, "", "", false)
			pdf.SetFont("Arial", "", 9)
			pdf.Ln(2)
			pdf.SetFont("Arial", "", 8)
			pdf.SetTextColor(100, 100, 100)
			statusText := fmt.Sprintf("Status: [UNVERIFIED] - %s", claim.Status)
			if claim.Evidence != "" {
				statusText += fmt.Sprintf("\nVerification Attempt: %s", claim.Evidence)
			}
			if len(claim.SourceURLs) > 0 {
				statusText += fmt.Sprintf("\nSources Checked: %s", strings.Join(claim.SourceURLs, ", "))
			}
			g.multiCell(pdf, 0, 4, statusText, "", "", false)
			pdf.SetTextColor(0, 0, 0)
			pdf.SetFont("Arial", "", 9)
			pdf.Ln(5)
		}
	}
}

func (g *Generator) addPositiveAnalysisPage(pdf *gofpdf.Fpdf, data *ReportData) {
	// Don't force page break - let it flow naturally

	pdf.SetFont("Arial", "B", 16)
	pdf.CellFormat(0, 12, "Steel Man Analysis", "", 0, "L", false, 0, "")
	pdf.Ln(15)

	if data.Positive == nil {
		pdf.SetFont("Arial", "I", 11)
		pdf.SetTextColor(128, 128, 128)
		pdf.MultiCell(0, 7, "Positive analysis not available.", "", "", false)
		return
	}

	// Strengths
	if len(data.Positive.Strengths) > 0 {
		pdf.SetFont("Arial", "B", 12)
		pdf.SetTextColor(0, 150, 0)
		pdf.CellFormat(0, 10, "Strengths", "", 0, "L", false, 0, "")
		pdf.Ln(10)
		pdf.SetTextColor(0, 0, 0)
		pdf.SetFont("Arial", "", 9)
		for _, strength := range data.Positive.Strengths {
			pdf.CellFormat(5, 6, "-", "", 0, "L", false, 0, "")
			g.multiCell(pdf, 0, 6, strength, "", "", false)
			pdf.Ln(3)
		}
		pdf.Ln(8)
	}

	// Opportunities
	if len(data.Positive.Opportunities) > 0 {
		pdf.SetFont("Arial", "B", 12)
		pdf.CellFormat(0, 10, "Opportunities", "", 0, "L", false, 0, "")
		pdf.Ln(10)
		pdf.SetFont("Arial", "", 9)
		for _, opp := range data.Positive.Opportunities {
			pdf.CellFormat(5, 6, "-", "", 0, "L", false, 0, "")
			g.multiCell(pdf, 0, 6, opp, "", "", false)
			pdf.Ln(3)
		}
		pdf.Ln(8)
	}

	// Value Proposition
	if data.Positive.ValueProposition != "" {
		pdf.SetFont("Arial", "B", 12)
		pdf.CellFormat(0, 10, "Value Proposition", "", 0, "L", false, 0, "")
		pdf.Ln(10)
		pdf.SetFont("Arial", "", 9)
		g.multiCell(pdf, 0, 6, data.Positive.ValueProposition, "", "", false)
		pdf.Ln(8)
	}

	// Innovation
	if len(data.Positive.Innovation) > 0 {
		pdf.SetFont("Arial", "B", 12)
		pdf.CellFormat(0, 10, "Innovation & Unique Aspects", "", 0, "L", false, 0, "")
		pdf.Ln(10)
		pdf.SetFont("Arial", "", 9)
		for _, innovation := range data.Positive.Innovation {
			pdf.CellFormat(5, 6, "-", "", 0, "L", false, 0, "")
			g.multiCell(pdf, 0, 6, innovation, "", "", false)
			pdf.Ln(3)
		}
	}
}

func (g *Generator) addSteelManningPage(pdf *gofpdf.Fpdf, data *ReportData) {
	// Don't force page break - let it flow naturally

	pdf.SetFont("Arial", "B", 16)
	pdf.SetTextColor(200, 0, 0)
	pdf.CellFormat(0, 12, "Critical Analysis", "", 0, "L", false, 0, "")
	pdf.Ln(15)
	pdf.SetTextColor(0, 0, 0)

	if data.SteelManning == nil {
		pdf.SetFont("Arial", "I", 11)
		pdf.SetTextColor(128, 128, 128)
		pdf.MultiCell(0, 7, "Steel manning analysis not available.", "", "", false)
		return
	}

	// Concerns
	if len(data.SteelManning.Concerns) > 0 {
		pdf.SetFont("Arial", "B", 12)
		pdf.CellFormat(0, 10, "Key Concerns", "", 0, "L", false, 0, "")
		pdf.Ln(10)
		pdf.SetFont("Arial", "", 9)
		for _, concern := range data.SteelManning.Concerns {
			pdf.CellFormat(5, 6, "-", "", 0, "L", false, 0, "")
			g.multiCell(pdf, 0, 6, concern, "", "", false)
			pdf.Ln(3)
		}
		pdf.Ln(8)
	}

	// Weaknesses
	if len(data.SteelManning.Weaknesses) > 0 {
		pdf.SetFont("Arial", "B", 12)
		pdf.CellFormat(0, 10, "Identified Weaknesses", "", 0, "L", false, 0, "")
		pdf.Ln(10)
		pdf.SetFont("Arial", "", 9)
		for _, weakness := range data.SteelManning.Weaknesses {
			pdf.CellFormat(5, 6, "-", "", 0, "L", false, 0, "")
			g.multiCell(pdf, 0, 6, weakness, "", "", false)
			pdf.Ln(3)
		}
		pdf.Ln(8)
	}

	// Red Flags
	if len(data.SteelManning.RedFlags) > 0 {
		pdf.SetFont("Arial", "B", 12)
		pdf.SetTextColor(200, 0, 0)
		pdf.CellFormat(0, 10, "Red Flags", "", 0, "L", false, 0, "")
		pdf.Ln(10)
		pdf.SetTextColor(0, 0, 0)
		pdf.SetFont("Arial", "", 9)
		for _, flag := range data.SteelManning.RedFlags {
			pdf.CellFormat(5, 6, "[!]", "", 0, "L", false, 0, "")
			g.multiCell(pdf, 0, 6, flag, "", "", false)
			pdf.Ln(3)
		}
		pdf.Ln(8)
	}

	// Alternatives
	if len(data.SteelManning.Alternatives) > 0 {
		pdf.SetFont("Arial", "B", 12)
		pdf.CellFormat(0, 10, "Alternative Approaches", "", 0, "L", false, 0, "")
		pdf.Ln(10)
		pdf.SetFont("Arial", "", 9)
		for _, alt := range data.SteelManning.Alternatives {
			pdf.CellFormat(5, 6, "-", "", 0, "L", false, 0, "")
			g.multiCell(pdf, 0, 6, alt, "", "", false)
			pdf.Ln(3)
		}
	}
}

func (g *Generator) addRecommendationsPage(pdf *gofpdf.Fpdf, data *ReportData) {
	// Don't force page break - let it flow naturally

	pdf.SetFont("Arial", "B", 16)
	pdf.CellFormat(0, 12, "Recommendations", "", 0, "L", false, 0, "")
	pdf.Ln(15)

	if data.Recommendations == nil {
		pdf.SetFont("Arial", "I", 11)
		pdf.SetTextColor(128, 128, 128)
		pdf.MultiCell(0, 7, "Recommendations not available.", "", "", false)
		return
	}

	// Verdict Section Title
	pdf.SetFont("Arial", "B", 16)
	pdf.CellFormat(0, 12, "Final Verdict", "", 0, "L", false, 0, "")
	pdf.Ln(15)

	// Idea Quality
	pdf.SetFont("Arial", "B", 12)
	pdf.CellFormat(0, 10, "Idea Quality Assessment", "", 0, "L", false, 0, "")
	pdf.Ln(8)
	pdf.SetFont("Arial", "", 10)
	ideaQuality := data.Recommendations.IdeaQuality
	if ideaQuality == "" {
		ideaQuality = "Uncertain"
	}
	qualityColor := 0x000000
	switch strings.ToLower(ideaQuality) {
	case "good":
		qualityColor = 0x00C853 // Green
	case "bad":
		qualityColor = 0xD32F2F // Red
	default:
		qualityColor = 0xFFA000 // Orange
	}
	pdf.SetTextColor(qualityColor>>16, (qualityColor>>8)&0xFF, qualityColor&0xFF)
	g.cellFormat(pdf, 0, 8, fmt.Sprintf("Assessment: %s", strings.ToUpper(ideaQuality)), "", 0, "L", false, 0, "")
	pdf.Ln(10)
	pdf.SetTextColor(0, 0, 0)

	// Team Capability
	pdf.SetFont("Arial", "B", 12)
	pdf.CellFormat(0, 10, "Team Capability Assessment", "", 0, "L", false, 0, "")
	pdf.Ln(8)
	pdf.SetFont("Arial", "", 10)
	teamCapability := data.Recommendations.TeamCapability
	if teamCapability == "" {
		teamCapability = "Uncertain"
	}
	capabilityColor := 0x000000
	switch strings.ToLower(teamCapability) {
	case "can deliver":
		capabilityColor = 0x00C853 // Green
	case "cannot deliver":
		capabilityColor = 0xD32F2F // Red
	default:
		capabilityColor = 0xFFA000 // Orange
	}
	pdf.SetTextColor(capabilityColor>>16, (capabilityColor>>8)&0xFF, capabilityColor&0xFF)
	g.cellFormat(pdf, 0, 8, fmt.Sprintf("Assessment: %s", strings.ToUpper(teamCapability)), "", 0, "L", false, 0, "")
	pdf.Ln(10)
	pdf.SetTextColor(0, 0, 0)

	// AI Vote Recommendation
	pdf.SetFont("Arial", "B", 12)
	pdf.CellFormat(0, 10, "AI Vote Recommendation", "", 0, "L", false, 0, "")
	pdf.Ln(8)
	pdf.SetFont("Arial", "", 10)
	aiVote := data.Recommendations.AIVote
	if aiVote == "" {
		aiVote = "Abstain"
	}
	voteColor := 0x000000
	switch strings.ToUpper(aiVote) {
	case "AYE", "YES":
		voteColor = 0x00C853 // Green
	case "NAY", "NO":
		voteColor = 0xD32F2F // Red
	default:
		voteColor = 0xFFA000 // Orange
	}
	pdf.SetTextColor(voteColor>>16, (voteColor>>8)&0xFF, voteColor&0xFF)
	pdf.SetFont("Arial", "B", 14)
	g.cellFormat(pdf, 0, 12, fmt.Sprintf("AI Vote: %s", strings.ToUpper(aiVote)), "", 0, "L", false, 0, "")
	pdf.Ln(12)
	pdf.SetTextColor(0, 0, 0)

	// Overall Verdict
	pdf.SetFont("Arial", "B", 12)
	pdf.CellFormat(0, 10, "Overall Recommendation", "", 0, "L", false, 0, "")
	pdf.Ln(8)
	pdf.SetFont("Arial", "B", 14)
	verdictColor := 0
	switch strings.ToLower(data.Recommendations.Verdict) {
	case "approve":
		verdictColor = 0x00C853 // Green
	case "deny":
		verdictColor = 0xD32F2F // Red
	case "modify":
		verdictColor = 0xFFA000 // Orange
	default:
		verdictColor = 0x000000 // Black
	}
	pdf.SetTextColor(verdictColor>>16, (verdictColor>>8)&0xFF, verdictColor&0xFF)
	g.cellFormat(pdf, 0, 12, fmt.Sprintf("Verdict: %s", strings.ToUpper(data.Recommendations.Verdict)), "", 0, "L", false, 0, "")
	pdf.Ln(12)
	pdf.SetTextColor(0, 0, 0)

	// Confidence
	pdf.SetFont("Arial", "B", 11)
	g.cellFormat(pdf, 0, 10, fmt.Sprintf("Confidence: %s", data.Recommendations.Confidence), "", 0, "L", false, 0, "")
	pdf.Ln(12)

	// Reasoning
	pdf.SetFont("Arial", "B", 11)
	pdf.CellFormat(0, 10, "Reasoning", "", 0, "L", false, 0, "")
	pdf.Ln(8)
	pdf.SetFont("Arial", "", 9)
	g.multiCell(pdf, 0, 6, data.Recommendations.Reasoning, "", "", false)
	pdf.Ln(10)

	// Key Points
	if len(data.Recommendations.KeyPoints) > 0 {
		pdf.SetFont("Arial", "B", 11)
		pdf.CellFormat(0, 10, "Key Points", "", 0, "L", false, 0, "")
		pdf.Ln(8)
		pdf.SetFont("Arial", "", 9)
		for _, point := range data.Recommendations.KeyPoints {
			pdf.CellFormat(5, 6, "-", "", 0, "L", false, 0, "")
			g.multiCell(pdf, 0, 6, point, "", "", false)
			pdf.Ln(3)
		}
		pdf.Ln(8)
	}

	// Conditions (if modifying)
	if data.Recommendations.Verdict == "Modify" && len(data.Recommendations.Conditions) > 0 {
		pdf.SetFont("Arial", "B", 11)
		pdf.CellFormat(0, 10, "Recommended Modifications", "", 0, "L", false, 0, "")
		pdf.Ln(8)
		pdf.SetFont("Arial", "", 9)
		for _, condition := range data.Recommendations.Conditions {
			pdf.CellFormat(5, 6, "-", "", 0, "L", false, 0, "")
			g.multiCell(pdf, 0, 6, condition, "", "", false)
			pdf.Ln(3)
		}
	}
}
