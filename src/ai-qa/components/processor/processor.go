package processor

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
)

type Processor struct {
	tempDir    string
	db         *gorm.DB
	httpClient *http.Client
}

func NewProcessor(tempDir string, db *gorm.DB) *Processor {
	os.MkdirAll(tempDir, 0755)
	return &Processor{
		tempDir: tempDir,
		db:      db,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (p *Processor) GetProposalContent(network string, refID uint32) (string, error) {
	cacheFile := p.getCacheFilePath(network, refID)

	if content, err := os.ReadFile(cacheFile); err == nil {
		return string(content), nil
	}

	err := p.RefreshProposal(network, refID)
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(cacheFile)
	if err != nil {
		return "", fmt.Errorf("failed to read cache after refresh: %w", err)
	}

	return string(content), nil
}

func (p *Processor) RefreshProposal(network string, refID uint32) error {
	networkLower := strings.ToLower(network)

	proposalContent, err := p.fetchProposalFromPolkassembly(networkLower, refID)
	if err != nil {
		return fmt.Errorf("fetch proposal: %w", err)
	}

	links := p.extractLinks(proposalContent)

	var fullContent strings.Builder
	fullContent.WriteString("# Proposal Content\n\n")
	fullContent.WriteString(proposalContent)
	fullContent.WriteString("\n\n")

	for _, link := range links {
		if p.isDocumentLink(link) {
			content, err := p.downloadDocument(link)
			if err != nil {
				log.Printf("Failed to download %s: %v", link, err)
				continue
			}
			fullContent.WriteString(fmt.Sprintf("\n\n## Document: %s\n\n", link))
			fullContent.WriteString(content)
		}
	}

	cacheFile := p.getCacheFilePath(network, refID)
	if err := os.WriteFile(cacheFile, []byte(fullContent.String()), 0644); err != nil {
		return fmt.Errorf("write cache: %w", err)
	}

	return nil
}

func (p *Processor) fetchProposalFromPolkassembly(network string, refID uint32) (string, error) {
	// Use the correct v2 API endpoint format for referenda
	url := fmt.Sprintf("https://%s.polkassembly.io/api/v2/posts/referenda/%d", network, refID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		// Try alternative endpoint format
		url = fmt.Sprintf("https://%s.polkassembly.io/api/v1/posts/on-chain-post?proposalType=referendums_v2&postId=%d", network, refID)

		req, err = http.NewRequest("GET", url, nil)
		if err != nil {
			return "", err
		}

		req.Header.Set("Accept", "application/json")

		resp2, err := p.httpClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp2.Body.Close()

		body, err = io.ReadAll(resp2.Body)
		if err != nil {
			return "", err
		}

		if resp2.StatusCode != http.StatusOK {
			return "", fmt.Errorf("API returned status %d and %d", resp.StatusCode, resp2.StatusCode)
		}
	}

	// First try to unmarshal as a post object
	var postResult struct {
		Post struct {
			Content     string `json:"content"`
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"post"`
	}

	if err := json.Unmarshal(body, &postResult); err == nil && postResult.Post.Content != "" {
		var content strings.Builder
		if postResult.Post.Title != "" {
			content.WriteString("Title: " + postResult.Post.Title + "\n\n")
		}
		if postResult.Post.Description != "" {
			content.WriteString("Description: " + postResult.Post.Description + "\n\n")
		}
		if postResult.Post.Content != "" {
			content.WriteString(postResult.Post.Content)
		}
		return content.String(), nil
	}

	// Try direct structure
	var directResult struct {
		Content     string `json:"content"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}

	if err := json.Unmarshal(body, &directResult); err == nil {
		var content strings.Builder
		if directResult.Title != "" {
			content.WriteString("Title: " + directResult.Title + "\n\n")
		}
		if directResult.Description != "" {
			content.WriteString("Description: " + directResult.Description + "\n\n")
		}
		if directResult.Content != "" {
			content.WriteString(directResult.Content)
		}

		if content.Len() > 0 {
			return content.String(), nil
		}
	}

	// Try to extract any text content from the response
	var genericResult map[string]interface{}
	if err := json.Unmarshal(body, &genericResult); err == nil {
		var content strings.Builder

		// Look for common fields
		fields := []string{"content", "description", "text", "body", "proposal", "details"}
		for _, field := range fields {
			if val, ok := genericResult[field]; ok {
				if strVal, ok := val.(string); ok && strVal != "" {
					content.WriteString(strVal + "\n\n")
				}
			}
		}

		// Check nested post object
		if post, ok := genericResult["post"].(map[string]interface{}); ok {
			for _, field := range fields {
				if val, ok := post[field]; ok {
					if strVal, ok := val.(string); ok && strVal != "" {
						content.WriteString(strVal + "\n\n")
					}
				}
			}
		}

		if content.Len() > 0 {
			return content.String(), nil
		}
	}

	return "", fmt.Errorf("unable to parse proposal content from API response")
}

func (p *Processor) extractLinks(content string) []string {
	var links []string
	seen := make(map[string]bool)

	urlRegex := regexp.MustCompile(`https?://[^\s<>"{}|\\^\[\]]+`)
	matches := urlRegex.FindAllString(content, -1)

	for _, match := range matches {
		match = strings.TrimRight(match, ".,;:!?)")
		if !seen[match] {
			seen[match] = true
			links = append(links, match)
		}
	}

	return links
}

func (p *Processor) isDocumentLink(link string) bool {
	docExtensions := []string{
		".pdf", ".doc", ".docx", ".txt", ".md", ".rtf",
		".odt", ".csv", ".json", ".xml",
	}

	linkLower := strings.ToLower(link)

	if strings.Contains(linkLower, "docs.google.com") ||
		strings.Contains(linkLower, "drive.google.com") {
		return true
	}

	for _, ext := range docExtensions {
		if strings.HasSuffix(linkLower, ext) {
			return true
		}
	}

	return false
}

func (p *Processor) downloadDocument(link string) (string, error) {
	if strings.Contains(link, "docs.google.com") {
		return p.downloadGoogleDoc(link)
	}

	if strings.Contains(link, "drive.google.com") {
		return p.downloadGoogleDriveFile(link)
	}

	return p.downloadGenericFile(link)
}

func (p *Processor) downloadGoogleDoc(link string) (string, error) {
	docID := p.extractGoogleDocID(link)
	if docID == "" {
		return "", fmt.Errorf("could not extract doc ID")
	}

	exportURL := fmt.Sprintf("https://docs.google.com/document/d/%s/export?format=txt", docID)

	resp, err := p.httpClient.Get(exportURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download: status %d", resp.StatusCode)
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, 500000))
	if err != nil {
		return "", err
	}

	contentStr := string(content)
	if len(contentStr) > 50000 {
		contentStr = contentStr[:50000] + "\n\n[Content truncated...]"
	}

	return contentStr, nil
}

func (p *Processor) downloadGoogleDriveFile(link string) (string, error) {
	fileID := p.extractGoogleDriveFileID(link)
	if fileID == "" {
		return "", fmt.Errorf("could not extract file ID")
	}

	downloadURL := fmt.Sprintf("https://drive.google.com/uc?export=download&id=%s", fileID)

	resp, err := p.httpClient.Get(downloadURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download: status %d", resp.StatusCode)
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, 500000))
	if err != nil {
		return "", err
	}

	contentStr := string(content)
	if !p.isTextContent(contentStr) {
		return "", fmt.Errorf("file appears to be binary")
	}

	if len(contentStr) > 50000 {
		contentStr = contentStr[:50000] + "\n\n[Content truncated...]"
	}

	return contentStr, nil
}

func (p *Processor) downloadGenericFile(link string) (string, error) {
	resp, err := p.httpClient.Get(link)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download: status %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "image") ||
		strings.Contains(contentType, "video") ||
		strings.Contains(contentType, "audio") ||
		strings.Contains(contentType, "application/octet-stream") {
		return "", fmt.Errorf("binary content type: %s", contentType)
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, 500000))
	if err != nil {
		return "", err
	}

	contentStr := string(content)
	if !p.isTextContent(contentStr) {
		return "", fmt.Errorf("file appears to be binary")
	}

	if len(contentStr) > 50000 {
		contentStr = contentStr[:50000] + "\n\n[Content truncated...]"
	}

	return contentStr, nil
}

func (p *Processor) extractGoogleDocID(link string) string {
	patterns := []string{
		`/document/d/([a-zA-Z0-9-_]+)`,
		`docId=([a-zA-Z0-9-_]+)`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(link)
		if len(matches) > 1 {
			return matches[1]
		}
	}

	return ""
}

func (p *Processor) extractGoogleDriveFileID(link string) string {
	patterns := []string{
		`/file/d/([a-zA-Z0-9-_]+)`,
		`id=([a-zA-Z0-9-_]+)`,
		`/open\?id=([a-zA-Z0-9-_]+)`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(link)
		if len(matches) > 1 {
			return matches[1]
		}
	}

	return ""
}

func (p *Processor) isTextContent(content string) bool {
	if len(content) == 0 {
		return false
	}

	nullCount := 0
	controlCount := 0
	checkLen := min(1000, len(content))

	for i := 0; i < checkLen; i++ {
		c := content[i]
		if c == 0 {
			nullCount++
		}
		if c < 32 && c != '\n' && c != '\r' && c != '\t' {
			controlCount++
		}
	}

	return nullCount < 5 && controlCount < 50
}

func (p *Processor) getCacheFilePath(network string, refID uint32) string {
	hash := md5.Sum([]byte(fmt.Sprintf("%s-%d", network, refID)))
	filename := fmt.Sprintf("%s-%d-%s.txt", network, refID, hex.EncodeToString(hash[:8]))
	return filepath.Join(p.tempDir, filename)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
