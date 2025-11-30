package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	proposalFileName  = "proposal.txt"
	metadataFileName  = "metadata.json"
	directoryFiles    = "files"
	directoryImages   = "images"
	directoryVideo    = "video"
	directoryAudio    = "audio"
	directoryOther    = "other"
	maxDocAttachments = 12
	maxBinAttachments = 12
	maxBinarySize     = 20 * 1024 * 1024 // 20 MB
)

// FileCategory represents the type of cached artifact.
type FileCategory string

const (
	FileCategoryDocument FileCategory = "files"
	FileCategoryImage    FileCategory = "images"
	FileCategoryVideo    FileCategory = "video"
	FileCategoryAudio    FileCategory = "audio"
	FileCategoryOther    FileCategory = "other"
)

// Attachment describes an auxiliary cached artifact.
type Attachment struct {
	Category    FileCategory `json:"category"`
	FileName    string       `json:"file"`
	SourceURL   string       `json:"sourceUrl"`
	ContentType string       `json:"contentType,omitempty"`
	Kind        string       `json:"kind,omitempty"`
	SizeBytes   int64        `json:"sizeBytes,omitempty"`
}

// Entry represents a cached referendum data set.
type Entry struct {
	Network      string       `json:"network"`
	RefID        uint32       `json:"refId"`
	ProposalFile string       `json:"proposalFile"`
	Attachments  []Attachment `json:"attachments"`
	RefreshedAt  time.Time    `json:"refreshedAt"`
	Claims       *ClaimsData  `json:"claims,omitempty"`
	TeamMembers  *TeamsData   `json:"teamMembers,omitempty"`
	Summary      *SummaryData `json:"summary,omitempty"`

	baseDir string
}

// ProposalPath returns the absolute path to the cached proposal text.
func (e *Entry) ProposalPath() string {
	return filepath.Join(e.baseDir, filepath.FromSlash(e.ProposalFile))
}

// AttachmentPath resolves an attachment to an absolute path.
func (e *Entry) AttachmentPath(att Attachment) string {
	return filepath.Join(e.baseDir, filepath.FromSlash(att.FileName))
}

// BaseDir returns the cache directory for this entry.
func (e *Entry) BaseDir() string {
	return e.baseDir
}

// Manager manages referendum cache lifecycle.
type Manager struct {
	root              string
	httpClient        *http.Client
	mu                sync.RWMutex
	pdfToolsAvailable bool
}

// NewManager creates a new cache manager rooted at cacheDir.
func NewManager(cacheDir string) (*Manager, error) {
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "govcomms-cache")
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}

	return &Manager{
		root: cacheDir,
		httpClient: &http.Client{
			Timeout: 45 * time.Second,
		},
		pdfToolsAvailable: hasPDFTools(),
	}, nil
}

// CacheRoot returns the base directory for cached referendums.
func (m *Manager) CacheRoot() string {
	return m.root
}

// Refresh downloads and stores the latest referendum data.
func (m *Manager) Refresh(network string, refID uint32) (*Entry, error) {
	if strings.TrimSpace(network) == "" {
		return nil, fmt.Errorf("network name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	finalPaths := m.cachePaths(network, refID)
	tempBase, err := os.MkdirTemp(m.root, "refresh-stage-*")
	if err != nil {
		return nil, fmt.Errorf("create temp cache dir: %w", err)
	}
	stagePaths := newCachePaths(tempBase)
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(tempBase)
		}
	}()

	if err := createCacheDirs(stagePaths); err != nil {
		return nil, err
	}

	networkLower := strings.ToLower(strings.TrimSpace(network))
	proposalContent, err := m.fetchProposalFromPolkassembly(networkLower, refID)
	if err != nil {
		return nil, fmt.Errorf("fetch proposal: %w", err)
	}

	var combined strings.Builder
	combined.WriteString("# Proposal Content\n\n")
	combined.WriteString(proposalContent)
	combined.WriteString("\n\n")

	links := extractLinks(proposalContent)
	attachments := m.processAttachments(stagePaths, links, &combined)

	if err := os.WriteFile(stagePaths.ProposalPath, []byte(combined.String()), 0o644); err != nil {
		return nil, fmt.Errorf("write proposal: %w", err)
	}

	entry := &Entry{
		Network:      network,
		RefID:        refID,
		ProposalFile: proposalFileName,
		Attachments:  attachments,
		RefreshedAt:  time.Now().UTC(),
		baseDir:      stagePaths.BaseDir,
	}

	if err := saveMetadata(stagePaths, entry); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(finalPaths.BaseDir), 0o755); err != nil {
		return nil, fmt.Errorf("prepare cache dir: %w", err)
	}

	if err := os.RemoveAll(finalPaths.BaseDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("clear cache dir: %w", err)
	}

	if err := os.Rename(stagePaths.BaseDir, finalPaths.BaseDir); err != nil {
		return nil, fmt.Errorf("activate cache dir: %w", err)
	}

	keepStage = true
	entry.baseDir = finalPaths.BaseDir

	return entry, nil
}

// GetProposalContent returns cached proposal text, refreshing if needed.
func (m *Manager) GetProposalContent(network string, refID uint32) (string, error) {
	entry, err := m.EnsureEntry(network, refID)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(entry.ProposalPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			entry, err = m.Refresh(network, refID)
			if err != nil {
				return "", err
			}
			data, err = os.ReadFile(entry.ProposalPath())
		}
	}
	if err != nil {
		return "", fmt.Errorf("read proposal: %w", err)
	}

	return string(data), nil
}

// EnsureEntry loads metadata or refreshes if absent.
func (m *Manager) EnsureEntry(network string, refID uint32) (*Entry, error) {
	// Try to load with read lock first
	m.mu.RLock()
	entry, err := m.loadEntryUnlocked(network, refID)
	m.mu.RUnlock()
	
	if err == nil {
		return entry, nil
	}

	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
		// Need write lock for refresh
		return m.Refresh(network, refID)
	}

	return nil, err
}

// loadEntryUnlocked loads metadata without acquiring locks (caller must hold lock).
func (m *Manager) loadEntryUnlocked(network string, refID uint32) (*Entry, error) {
	paths := m.cachePaths(network, refID)
	data, err := os.ReadFile(paths.MetadataPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
			return nil, fs.ErrNotExist
		}
		return nil, fmt.Errorf("read metadata: %w", err)
	}

	var stored metadataRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("parse metadata: %w", err)
	}

	entry := &Entry{
		Network:      firstNonEmpty(stored.Network, network),
		RefID:        valueOrDefault(stored.RefID, refID),
		ProposalFile: stored.ProposalFile,
		Attachments:  stored.Attachments,
		RefreshedAt:  stored.RefreshedAt,
		Claims:       stored.Claims,
		TeamMembers:  stored.TeamMembers,
		Summary:      stored.Summary,
		baseDir:      paths.BaseDir,
	}

	if entry.ProposalFile == "" {
		entry.ProposalFile = proposalFileName
	}

	return entry, nil
}

// loadEntry loads metadata for a cached referendum.
func (m *Manager) loadEntry(network string, refID uint32) (*Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loadEntryUnlocked(network, refID)
}

// UpdateResearchData updates the cache entry with claims and team analysis data.
func (m *Manager) UpdateResearchData(network string, refID uint32, claims *ClaimsData, teamMembers *TeamsData) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, err := m.loadEntryUnlocked(network, refID)
	if err != nil {
		return fmt.Errorf("load entry: %w", err)
	}

	entry.Claims = claims
	entry.TeamMembers = teamMembers

	paths := m.cachePaths(network, refID)
	return saveMetadata(paths, entry)
}

// UpdateSummary updates the cache entry with summary data.
func (m *Manager) UpdateSummary(network string, refID uint32, summary *SummaryData) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, err := m.loadEntryUnlocked(network, refID)
	if err != nil {
		return fmt.Errorf("load entry: %w", err)
	}

	entry.Summary = summary

	paths := m.cachePaths(network, refID)
	return saveMetadata(paths, entry)
}

func (m *Manager) cachePaths(network string, refID uint32) cachePaths {
	networkSegment := sanitizeSegment(network)
	refSegment := fmt.Sprintf("%d", refID)
	base := filepath.Join(m.root, networkSegment, refSegment)

	return newCachePaths(base)
}

func newCachePaths(base string) cachePaths {
	return cachePaths{
		BaseDir:      base,
		ProposalPath: filepath.Join(base, proposalFileName),
		MetadataPath: filepath.Join(base, metadataFileName),
		FilesDir:     filepath.Join(base, directoryFiles),
		ImagesDir:    filepath.Join(base, directoryImages),
		VideoDir:     filepath.Join(base, directoryVideo),
		AudioDir:     filepath.Join(base, directoryAudio),
		OtherDir:     filepath.Join(base, directoryOther),
	}
}

func createCacheDirs(paths cachePaths) error {
	dirs := []string{
		paths.BaseDir,
		paths.FilesDir,
		paths.ImagesDir,
		paths.VideoDir,
		paths.AudioDir,
		paths.OtherDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create cache dir %s: %w", dir, err)
		}
	}

	return nil
}

func saveMetadata(paths cachePaths, entry *Entry) error {
	record := metadataRecord{
		Network:      entry.Network,
		RefID:        entry.RefID,
		ProposalFile: entry.ProposalFile,
		Attachments:  entry.Attachments,
		RefreshedAt:  entry.RefreshedAt,
		Claims:       entry.Claims,
		TeamMembers:  entry.TeamMembers,
		Summary:      entry.Summary,
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	if err := os.WriteFile(paths.MetadataPath, data, 0o644); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}

	return nil
}

type metadataRecord struct {
	Network      string       `json:"network"`
	RefID        uint32       `json:"refId"`
	ProposalFile string       `json:"proposalFile"`
	Attachments  []Attachment `json:"attachments"`
	RefreshedAt  time.Time    `json:"refreshedAt"`
	// Research data
	Claims      *ClaimsData  `json:"claims,omitempty"`
	TeamMembers *TeamsData   `json:"teamMembers,omitempty"`
	Summary     *SummaryData `json:"summary,omitempty"`
}

// SummaryData stores the generated referendum summary
type SummaryData struct {
	Network           string        `json:"network"`
	RefID             uint32        `json:"refId"`
	Title             string        `json:"title"`
	BackgroundContext string        `json:"backgroundContext"`
	Summary           string        `json:"summary"`
	ValidClaims       []string      `json:"validClaims"`
	UnverifiedClaims  []string      `json:"unverifiedClaims"`
	InvalidClaims     []string      `json:"invalidClaims"`
	TeamMembers       []TeamSummary  `json:"teamMembers"`
	ProviderCompany   string        `json:"providerCompany"`
	AIModel           string        `json:"aiModel"`
	GeneratedAt       time.Time     `json:"generatedAt"`
}

// TeamSummary represents a team member in the summary
type TeamSummary struct {
	Name            string `json:"name"`
	Role            string `json:"role"`
	IsReal          bool   `json:"isReal"`
	HasStatedSkills bool   `json:"hasStatedSkills"`
	History         string `json:"history"` // 2 sentence description
}

// ClaimsData stores claim verification results
type ClaimsData struct {
	ProviderCompany string        `json:"providerCompany"`
	AIModel         string        `json:"aiModel"`
	ProcessedAt     time.Time     `json:"processedAt"`
	TotalClaims     int           `json:"totalClaims"`
	Results         []ClaimResult `json:"results"`
}

// ClaimResult represents a single verified claim
type ClaimResult struct {
	Claim      string   `json:"claim"`
	Category   string   `json:"category"`
	URLs       []string `json:"urls,omitempty"`
	Context    string   `json:"context,omitempty"`
	Status     string   `json:"status"` // Valid, Rejected, Unknown
	Evidence   string   `json:"evidence"`
	SourceURLs []string `json:"sourceUrls,omitempty"`
}

// TeamsData stores team member analysis results
type TeamsData struct {
	ProviderCompany string           `json:"providerCompany"`
	AIModel         string           `json:"aiModel"`
	ProcessedAt     time.Time        `json:"processedAt"`
	Members         []TeamMemberData `json:"members"`
}

// TeamMemberData represents a single analyzed team member
type TeamMemberData struct {
	Name            string   `json:"name"`
	Role            string   `json:"role"`
	IsReal          *bool    `json:"isReal,omitempty"`
	HasStatedSkills *bool    `json:"hasStatedSkills,omitempty"`
	Capability      string   `json:"capability,omitempty"`
	GitHub          []string `json:"github,omitempty"`
	Twitter         []string `json:"twitter,omitempty"`
	LinkedIn        []string `json:"linkedIn,omitempty"`
	Other           []string `json:"other,omitempty"`
	VerifiedURLs    []string `json:"verifiedUrls,omitempty"`
}

type cachePaths struct {
	BaseDir      string
	ProposalPath string
	MetadataPath string
	FilesDir     string
	ImagesDir    string
	VideoDir     string
	AudioDir     string
	OtherDir     string
}

func sanitizeSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}

	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '/' || r == '\\' || r == '.':
			b.WriteRune('-')
		}
	}

	if b.Len() == 0 {
		return "unknown"
	}

	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func valueOrDefault(val, defaultVal uint32) uint32 {
	if val == 0 {
		return defaultVal
	}
	return val
}

func (m *Manager) processAttachments(paths cachePaths, links []string, builder *strings.Builder) []Attachment {
	attachments := make([]Attachment, 0, len(links))
	counters := map[FileCategory]int{}

	for _, link := range links {
		if shouldSkipLink(link) {
			continue
		}

		category := classifyLink(link)
		switch category {
		case FileCategoryDocument:
			if counters[FileCategoryDocument] >= maxDocAttachments {
				continue
			}

			doc, err := m.downloadDocument(link)
			if err != nil {
				log.Printf("cache: document download failed %s: %v", link, err)
				continue
			}

			counters[FileCategoryDocument]++
			fileName := fmt.Sprintf("doc-%02d.txt", counters[FileCategoryDocument])
			fullPath := filepath.Join(paths.FilesDir, fileName)
			if err := os.WriteFile(fullPath, []byte(doc.Content), 0o644); err != nil {
				log.Printf("cache: write doc cache failed %s: %v", link, err)
				continue
			}

			builder.WriteString(fmt.Sprintf("\n\n## Document: %s\n\n", link))
			builder.WriteString(doc.Content)

			attachments = append(attachments, Attachment{
				Category:    FileCategoryDocument,
				FileName:    toRelative(directoryFiles, fileName),
				SourceURL:   link,
				ContentType: "text/plain",
				Kind:        doc.Kind,
				SizeBytes:   int64(len(doc.Content)),
			})
		case FileCategoryImage, FileCategoryVideo, FileCategoryAudio, FileCategoryOther:
			if counters[category] >= maxBinAttachments {
				continue
			}

			data, contentType, ext, err := m.downloadBinary(link, maxBinarySize)
			if err != nil {
				log.Printf("cache: binary download failed %s: %v", link, err)
				continue
			}

			counters[category]++
			prefix := string(category[:1])
			if category == FileCategoryImage {
				prefix = "image"
			} else if category == FileCategoryVideo {
				prefix = "video"
			} else if category == FileCategoryAudio {
				prefix = "audio"
			} else {
				prefix = "file"
			}

			fileName := fmt.Sprintf("%s-%02d%s", prefix, counters[category], ext)
			dir := paths.OtherDir
			dirName := directoryOther

			switch category {
			case FileCategoryImage:
				dir = paths.ImagesDir
				dirName = directoryImages
			case FileCategoryVideo:
				dir = paths.VideoDir
				dirName = directoryVideo
			case FileCategoryAudio:
				dir = paths.AudioDir
				dirName = directoryAudio
			}

			fullPath := filepath.Join(dir, fileName)
			if err := os.WriteFile(fullPath, data, 0o644); err != nil {
				log.Printf("cache: write attachment failed %s: %v", link, err)
				continue
			}

			attachments = append(attachments, Attachment{
				Category:    category,
				FileName:    toRelative(dirName, fileName),
				SourceURL:   link,
				ContentType: contentType,
				SizeBytes:   int64(len(data)),
			})

			summaryName := fmt.Sprintf("%s-summary-%02d.txt", prefix, counters[category])
			summaryPath := filepath.Join(paths.FilesDir, summaryName)
			summaryContent := fmt.Sprintf("Attachment summary\n\n"+
				"- Original URL: %s\n- Cached path: %s\n- Category: %s\n- Content-Type: %s\n- Size: %d bytes\n"+
				"Use this summary to describe the attachment without downloading the binary.",
				link, toRelative(dirName, fileName), category, contentType, len(data))
			if err := os.WriteFile(summaryPath, []byte(summaryContent), 0o644); err != nil {
				log.Printf("cache: write attachment summary failed %s: %v", link, err)
				continue
			}
			attachments = append(attachments, Attachment{
				Category:    FileCategoryDocument,
				FileName:    toRelative(directoryFiles, summaryName),
				SourceURL:   link,
				ContentType: "text/plain",
				Kind:        "summary",
				SizeBytes:   int64(len(summaryContent)),
			})
		default:
			continue
		}
	}

	if counters[FileCategoryDocument] == 0 && len(links) > 0 {
		builder.WriteString("\n\n*Note: Additional documents were linked but could not be extracted as text.*")
	}

	return attachments
}

func toRelative(dirName, fileName string) string {
	return filepath.ToSlash(filepath.Join(dirName, fileName))
}
