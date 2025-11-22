package cache

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	googleDocStandardPattern  = regexp.MustCompile(`/document/(?:u/\d+/)?d/([a-zA-Z0-9-_]+)`)
	googleDocPublishedPattern = regexp.MustCompile(`/document/d/e/([a-zA-Z0-9-_]+)`)
	googleDocQueryPattern     = regexp.MustCompile(`(?i)[?&](?:id|docid)=([a-zA-Z0-9-_]+)`)
)

type documentPayload struct {
	Content string
	Kind    string
}

func (m *Manager) fetchProposalFromPolkassembly(network string, refID uint32) (string, error) {
	url := fmt.Sprintf("https://%s.polkassembly.io/api/v2/posts/referenda/%d", network, refID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		url = fmt.Sprintf("https://%s.polkassembly.io/api/v1/posts/on-chain-post?proposalType=referendums_v2&postId=%d", network, refID)
		req, err = http.NewRequest("GET", url, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/json")

		resp2, err := m.httpClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp2.Body.Close()

		body, err = io.ReadAll(resp2.Body)
		if err != nil {
			return "", err
		}

		if resp2.StatusCode != http.StatusOK {
			return "", fmt.Errorf("polkassembly returned status %d and %d", resp.StatusCode, resp2.StatusCode)
		}
	}

	type postPayload struct {
		Post struct {
			Content     string `json:"content"`
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"post"`
	}

	var parsed postPayload
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Post.Content != "" {
		var b strings.Builder
		if parsed.Post.Title != "" {
			b.WriteString("Title: " + parsed.Post.Title + "\n\n")
		}
		if parsed.Post.Description != "" {
			b.WriteString("Description: " + parsed.Post.Description + "\n\n")
		}
		b.WriteString(parsed.Post.Content)
		return b.String(), nil
	}

	var direct struct {
		Content     string `json:"content"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}

	if err := json.Unmarshal(body, &direct); err == nil {
		var b strings.Builder
		if direct.Title != "" {
			b.WriteString("Title: " + direct.Title + "\n\n")
		}
		if direct.Description != "" {
			b.WriteString("Description: " + direct.Description + "\n\n")
		}
		b.WriteString(direct.Content)
		if b.Len() > 0 {
			return b.String(), nil
		}
	}

	var generic map[string]interface{}
	if err := json.Unmarshal(body, &generic); err == nil {
		var b strings.Builder
		fields := []string{"content", "description", "text", "body", "proposal", "details"}
		for _, field := range fields {
			if val, ok := generic[field]; ok {
				if str, ok := val.(string); ok && str != "" {
					b.WriteString(str + "\n\n")
				}
			}
		}

		if post, ok := generic["post"].(map[string]interface{}); ok {
			for _, field := range fields {
				if val, ok := post[field]; ok {
					if str, ok := val.(string); ok && str != "" {
						b.WriteString(str + "\n\n")
					}
				}
			}
		}

		if b.Len() > 0 {
			return b.String(), nil
		}
	}

	return "", fmt.Errorf("unable to parse proposal content from API response")
}

func extractLinks(content string) []string {
	var links []string
	seen := make(map[string]bool)

	urlRegex := regexp.MustCompile(`https?://[^\s<>"{}|\\^\[\]]+`)
	matches := urlRegex.FindAllString(content, -1)

	for _, match := range matches {
		match = strings.TrimRight(match, ".,;:!?)")
		if match == "" {
			continue
		}

		if !seen[match] {
			seen[match] = true
			links = append(links, match)
		}
	}

	return links
}

var (
	privateIPBlocks  []*net.IPNet
	blockedHostnames = map[string]struct{}{
		"localhost":                 {},
		"metadata.google.internal":  {},
		"metadata.google.internal.": {},
	}
)

func init() {
	for _, cidr := range []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
		"::/128",
	} {
		_, block, err := net.ParseCIDR(cidr)
		if err == nil {
			privateIPBlocks = append(privateIPBlocks, block)
		}
	}
}

func shouldSkipLink(link string) bool {
	lower := strings.ToLower(link)
	skipDomains := []string{
		"twitter.com", "x.com", "facebook.com", "instagram.com",
		"youtube.com", "youtu.be", "reddit.com", "github.com/issues",
		"polkadot.subsquare.io", "kusama.subsquare.io",
	}

	for _, domain := range skipDomains {
		if strings.Contains(lower, domain) {
			return true
		}
	}

	if !isSafeAttachmentURL(link) {
		log.Printf("cache: refusing to fetch unsafe link %s", link)
		return true
	}

	return false
}

func isSafeAttachmentURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return false
	}
	if _, blocked := blockedHostnames[host]; blocked {
		return false
	}

	if ip := net.ParseIP(host); ip != nil {
		return isPublicIP(ip)
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return false
	}
	for _, resolved := range ips {
		if !isPublicIP(resolved) {
			return false
		}
	}
	return true
}

func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return false
	}
	for _, block := range privateIPBlocks {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

func classifyLink(link string) FileCategory {
	lower := strings.ToLower(link)

	if strings.Contains(lower, "docs.google.com") ||
		strings.Contains(lower, "drive.google.com") {
		return FileCategoryDocument
	}

	documentExt := []string{".pdf", ".txt", ".md", ".rtf", ".odt", ".doc", ".docx"}
	for _, ext := range documentExt {
		if strings.HasSuffix(lower, ext) {
			return FileCategoryDocument
		}
	}

	imageExt := []string{".jpg", ".jpeg", ".png", ".gif", ".bmp", ".svg", ".webp"}
	for _, ext := range imageExt {
		if strings.HasSuffix(lower, ext) {
			return FileCategoryImage
		}
	}

	videoExt := []string{".mp4", ".avi", ".mov", ".webm", ".mkv"}
	for _, ext := range videoExt {
		if strings.HasSuffix(lower, ext) {
			return FileCategoryVideo
		}
	}

	audioExt := []string{".mp3", ".wav", ".flac", ".aac", ".ogg"}
	for _, ext := range audioExt {
		if strings.HasSuffix(lower, ext) {
			return FileCategoryAudio
		}
	}

	return FileCategoryOther
}

func (m *Manager) downloadDocument(link string) (documentPayload, error) {
	lower := strings.ToLower(link)

	if strings.HasSuffix(lower, ".pdf") || strings.Contains(lower, "drive.google.com/file") {
		if !m.pdfSupported() {
			return documentPayload{}, fmt.Errorf("pdf extraction tools not available")
		}
		text, err := m.downloadPDF(link)
		if err != nil {
			return documentPayload{}, err
		}
		return documentPayload{Content: text, Kind: "pdf"}, nil
	}

	if strings.Contains(lower, "docs.google.com") {
		text, err := m.downloadGoogleDoc(link)
		if err != nil {
			return documentPayload{}, err
		}
		return documentPayload{Content: text, Kind: "gdoc"}, nil
	}

	text, err := m.downloadGenericFile(link)
	if err != nil {
		return documentPayload{}, err
	}
	return documentPayload{Content: text, Kind: "text"}, nil
}

func (m *Manager) pdfSupported() bool {
	if m.pdfToolsAvailable {
		return true
	}
	if hasPDFTools() {
		m.pdfToolsAvailable = true
		return true
	}
	return false
}

func hasPDFTools() bool {
	_, err := exec.LookPath("pdftotext")
	return err == nil
}

func (m *Manager) downloadPDF(link string) (string, error) {
	hash := md5.Sum([]byte(link))
	fileName := fmt.Sprintf("temp_%s.pdf", hex.EncodeToString(hash[:4]))
	tempPDF := filepath.Join(os.TempDir(), fileName)
	defer os.Remove(tempPDF)

	resp, err := m.httpClient.Get(link)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download PDF: status %d", resp.StatusCode)
	}

	file, err := os.Create(tempPDF)
	if err != nil {
		return "", err
	}

	if _, err := io.Copy(file, resp.Body); err != nil {
		file.Close()
		return "", err
	}
	file.Close()

	cmd := exec.Command("pdftotext", "-layout", "-nopgbrk", "-enc", "UTF-8", tempPDF, "-")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	text := strings.ReplaceAll(string(output), "\x00", "")
	text = strings.TrimSpace(text)

	if len(text) < 100 {
		return "", fmt.Errorf("extracted text too short")
	}
	if len(text) > 50000 {
		text = text[:50000] + "\n\n[PDF content truncated...]"
	}

	return text, nil
}

func (m *Manager) downloadGoogleDoc(link string) (string, error) {
	exportURL, err := buildGoogleDocExportURL(link)
	if err != nil {
		return "", err
	}

	resp, err := m.httpClient.Get(exportURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download Google Doc: status %d", resp.StatusCode)
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, 500000))
	if err != nil {
		return "", err
	}

	text := strings.ReplaceAll(string(content), "\x00", "")
	text = strings.TrimSpace(text)
	if len(text) > 50000 {
		text = text[:50000] + "\n\n[Document content truncated...]"
	}

	return text, nil
}

func buildGoogleDocExportURL(link string) (string, error) {
	if matches := googleDocPublishedPattern.FindStringSubmatch(link); len(matches) > 1 {
		return fmt.Sprintf("https://docs.google.com/document/d/e/%s/pub?format=txt", matches[1]), nil
	}

	if matches := googleDocStandardPattern.FindStringSubmatch(link); len(matches) > 1 {
		return fmt.Sprintf("https://docs.google.com/document/d/%s/export?format=txt", matches[1]), nil
	}

	if matches := googleDocQueryPattern.FindStringSubmatch(link); len(matches) > 1 {
		return fmt.Sprintf("https://docs.google.com/document/d/%s/export?format=txt", matches[1]), nil
	}

	return "", fmt.Errorf("could not extract Google Doc ID")
}

func (m *Manager) downloadGenericFile(link string) (string, error) {
	resp, err := m.httpClient.Get(link)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download: status %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "pdf") ||
		strings.Contains(contentType, "image") ||
		strings.Contains(contentType, "video") ||
		strings.Contains(contentType, "audio") ||
		strings.Contains(contentType, "application/octet-stream") ||
		strings.Contains(contentType, "application/zip") {
		return "", fmt.Errorf("binary content type: %s", contentType)
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, 100000))
	if err != nil {
		return "", err
	}

	text := strings.ReplaceAll(string(content), "\x00", "")
	text = strings.TrimSpace(text)
	if !isTextContent(text) {
		return "", fmt.Errorf("file appears to be binary")
	}
	if len(text) > 50000 {
		text = text[:50000] + "\n\n[Content truncated...]"
	}

	return text, nil
}

func (m *Manager) downloadBinary(link string, limit int64) ([]byte, string, string, error) {
	resp, err := m.httpClient.Get(link)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", "", fmt.Errorf("failed to download: status %d", resp.StatusCode)
	}

	reader := io.LimitReader(resp.Body, limit+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", "", err
	}
	if int64(len(data)) > limit {
		return nil, "", "", fmt.Errorf("file exceeds limit of %d bytes", limit)
	}

	contentType := resp.Header.Get("Content-Type")
	ext := extensionFor(link, contentType)

	return data, contentType, ext, nil
}

func extensionFor(link, contentType string) string {
	lower := strings.ToLower(link)
	if dot := strings.LastIndex(lower, "."); dot != -1 && dot+1 < len(lower) {
		ext := lower[dot:]
		if len(ext) <= 5 {
			return ext
		}
	}

	switch {
	case strings.Contains(contentType, "png"):
		return ".png"
	case strings.Contains(contentType, "jpeg"):
		return ".jpg"
	case strings.Contains(contentType, "gif"):
		return ".gif"
	case strings.Contains(contentType, "webp"):
		return ".webp"
	case strings.Contains(contentType, "mp4"):
		return ".mp4"
	case strings.Contains(contentType, "webm"):
		return ".webm"
	case strings.Contains(contentType, "ogg"):
		return ".ogg"
	case strings.Contains(contentType, "wav"):
		return ".wav"
	}

	return ".bin"
}

func isTextContent(content string) bool {
	if len(content) == 0 {
		return false
	}

	checkLen := min(1000, len(content))
	nullCount := 0
	controlCount := 0
	printableCount := 0

	for i := 0; i < checkLen; i++ {
		c := content[i]
		if c == 0 {
			nullCount++
		} else if c >= 32 && c <= 126 {
			printableCount++
		} else if c == '\n' || c == '\r' || c == '\t' {
			// allowed
		} else if c < 32 || c > 126 {
			controlCount++
		}
	}

	printableRatio := float64(printableCount) / float64(checkLen)
	return nullCount == 0 && controlCount < 50 && printableRatio > 0.7
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
