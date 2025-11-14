package discord

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
)

const (
	MaxDiscordMessageLen = 2000
	SafeChunkLen         = 1900

	maxLinkButtons     = 25
	maxButtonLabelRune = 80
)

// BuildLongMessages formats a long message for Discord by chunking across messages.
func BuildLongMessages(message string, userID string) []string {
	mention := ""
	if userID != "" {
		mention = fmt.Sprintf("<@%s> ", userID)
	}

	firstMessage := mention + message
	if len(firstMessage) <= MaxDiscordMessageLen {
		return []string{firstMessage}
	}

	chunks := splitMessage(message, userID)
	for i := 1; i < len(chunks)-1; i++ {
		chunks[i] = chunks[i] + "\n*(continued...)*"
	}
	if len(chunks) > 1 {
		chunks[len(chunks)-1] = chunks[len(chunks)-1] + "\n*(end of response)*"
	}
	return chunks
}

func splitMessage(message string, userID string) []string {
	var messages []string
	mention := ""
	if userID != "" {
		mention = fmt.Sprintf("<@%s> ", userID)
	}
	firstMaxLength := SafeChunkLen - len(mention)
	paragraphs := strings.Split(message, "\n\n")

	var currentMessage strings.Builder
	isFirst := true

	for _, paragraph := range paragraphs {
		if len(paragraph) > SafeChunkLen {
			if currentMessage.Len() > 0 {
				if isFirst {
					messages = append(messages, mention+currentMessage.String())
					isFirst = false
				} else {
					messages = append(messages, currentMessage.String())
				}
				currentMessage.Reset()
			}

			sentences := splitBySentences(paragraph)
			for _, sentence := range sentences {
				effectiveMaxLength := SafeChunkLen
				if isFirst {
					effectiveMaxLength = firstMaxLength
				}
				if currentMessage.Len()+len(sentence)+2 > effectiveMaxLength {
					if currentMessage.Len() > 0 {
						if isFirst {
							messages = append(messages, mention+currentMessage.String())
							isFirst = false
						} else {
							messages = append(messages, currentMessage.String())
						}
						currentMessage.Reset()
					}
				}
				if currentMessage.Len() > 0 {
					currentMessage.WriteString(" ")
				}
				currentMessage.WriteString(sentence)
			}
		} else {
			effectiveMaxLength := SafeChunkLen
			if isFirst {
				effectiveMaxLength = firstMaxLength
			}
			if currentMessage.Len()+len(paragraph)+4 > effectiveMaxLength {
				if currentMessage.Len() > 0 {
					if isFirst {
						messages = append(messages, mention+currentMessage.String())
						isFirst = false
					} else {
						messages = append(messages, currentMessage.String())
					}
					currentMessage.Reset()
				}
			}
			if currentMessage.Len() > 0 {
				currentMessage.WriteString("\n\n")
			}
			currentMessage.WriteString(paragraph)
		}
	}

	if currentMessage.Len() > 0 {
		if isFirst {
			messages = append(messages, mention+currentMessage.String())
		} else {
			messages = append(messages, currentMessage.String())
		}
	}

	return messages
}

func splitBySentences(text string) []string {
	var sentences []string
	var current strings.Builder
	for _, char := range text {
		current.WriteRune(char)
		if char == '.' || char == '!' || char == '?' {
			sentences = append(sentences, strings.TrimSpace(current.String()))
			current.Reset()
		}
	}
	if current.Len() > 0 {
		sentences = append(sentences, strings.TrimSpace(current.String()))
	}
	if len(sentences) == 0 || (len(sentences) == 1 && len(sentences[0]) > SafeChunkLen) {
		words := strings.Fields(text)
		var chunks []string
		var chunk strings.Builder
		for _, word := range words {
			if chunk.Len()+len(word)+1 > SafeChunkLen {
				chunks = append(chunks, chunk.String())
				chunk.Reset()
			}
			if chunk.Len() > 0 {
				chunk.WriteString(" ")
			}
			chunk.WriteString(word)
		}
		if chunk.Len() > 0 {
			chunks = append(chunks, chunk.String())
		}
		return chunks
	}
	return sentences
}

var (
	newlineCollapse   = regexp.MustCompile(`\n{3,}`)
	markdownLinkRegex = regexp.MustCompile(`\[(?P<label>[^\]]+)\]\((?P<url>https?://[^\s)<>]+)\)`)
	bareURLRegex      = regexp.MustCompile(`https?://[^\s\[\]()<>]+`)
	doubleSpaceRegex  = regexp.MustCompile(` {2,}`)
)

// BeautifyForDiscord normalizes AI-responses for improved readability.
func BeautifyForDiscord(text string) string {
	if text == "" {
		return text
	}

	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = newlineCollapse.ReplaceAllString(normalized, "\n\n")

	lines := strings.Split(normalized, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "- "):
			lines[i] = strings.Replace(line, "- ", "• ", 1)
		case strings.HasPrefix(trimmed, "* "):
			lines[i] = strings.Replace(line, "* ", "• ", 1)
		}
	}

	result := strings.Join(lines, "\n")
	result = strings.TrimSpace(result)
	return WrapURLsNoEmbed(result)
}

// StyledMessage represents a formatted block plus optional link buttons.
type StyledMessage struct {
	Content    string
	Components []discordgo.MessageComponent
}

var wrappedURLRegex = regexp.MustCompile(`<https?://[^\s<>]+>`)

// BuildStyledMessages formats content with a consistent Markdown layout and splits it into Discord-safe chunks.
func BuildStyledMessages(title string, body string, userID string) []StyledMessage {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}

	cleaned := BeautifyForDiscord(body)
	chunks := splitMessage(cleaned, "")
	if len(chunks) == 0 {
		chunks = []string{cleaned}
	}

	var messages []StyledMessage
	for idx, chunk := range chunks {
		currentTitle := title
		if idx > 0 {
			currentTitle = ""
		}

		payload := buildStyledMessageFromCleanChunk(currentTitle, chunk)

		if len(chunks) > 1 {
			if idx < len(chunks)-1 {
				payload.Content += "\n*(continued...)*"
			} else {
				payload.Content += "\n*(end of response)*"
			}
		}

		if idx == 0 && userID != "" {
			payload.Content = fmt.Sprintf("<@%s>\n%s", userID, payload.Content)
		}

		messages = append(messages, payload)
	}

	return messages
}

// BuildStyledMessage produces a single styled message (no chunking) with optional link buttons.
func BuildStyledMessage(title string, body string) StyledMessage {
	cleaned := BeautifyForDiscord(strings.TrimSpace(body))
	if cleaned == "" {
		cleaned = "_No content_"
	}
	return buildStyledMessageFromCleanChunk(title, cleaned)
}

// FormatStyledBlock returns a single styled block suitable for shorter Discord messages.
func FormatStyledBlock(title string, body string) string {
	return BuildStyledMessage(title, body).Content
}

type linkReference struct {
	Index   int
	URL     string
	Display string
}

// ReplaceURLsAndCollect strips URLs or markdown links from text and returns cleaned text plus references.
func ReplaceURLsAndCollect(input string) (string, []linkReference) {
	return replaceURLsWithReferences(input)
}

func buildStyledMessageFromCleanChunk(title string, cleanedBody string) StyledMessage {
	trimmed := strings.TrimSpace(cleanedBody)
	if trimmed == "" {
		trimmed = "_No content_"
	}

	withoutURLs, refs := replaceURLsWithReferences(trimmed)
	content := formatSimpleBlock(title, withoutURLs)

	return StyledMessage{
		Content:    content,
		Components: buildLinkButtons(refs),
	}
}

func replaceURLsWithReferences(input string) (string, []linkReference) {
	var refs []linkReference
	seen := make(map[string]int)

	text := markdownLinkRegex.ReplaceAllStringFunc(input, func(match string) string {
		submatches := markdownLinkRegex.FindStringSubmatch(match)
		if len(submatches) < 3 {
			return match
		}
		label := submatches[1]
		urlStr := strings.TrimSpace(submatches[2])
		if urlStr == "" {
			return label
		}
		if _, ok := seen[urlStr]; !ok {
			ref := linkReference{
				Index:   len(refs) + 1,
				URL:     urlStr,
				Display: summarizeURLDisplay(urlStr),
			}
			refs = append(refs, ref)
			seen[urlStr] = len(refs) - 1
		}
		return label
	})

	builder := strings.Builder{}
	last := 0
	matches := bareURLRegex.FindAllStringIndex(text, -1)
	for _, match := range matches {
		builder.WriteString(text[last:match[0]])
		urlStr := strings.TrimSpace(text[match[0]:match[1]])
		if urlStr != "" {
			if _, ok := seen[urlStr]; !ok {
				ref := linkReference{
					Index:   len(refs) + 1,
					URL:     urlStr,
					Display: summarizeURLDisplay(urlStr),
				}
				refs = append(refs, ref)
				seen[urlStr] = len(refs) - 1
			}
		}
		last = match[1]
	}
	builder.WriteString(text[last:])

	cleaned := doubleSpaceRegex.ReplaceAllString(builder.String(), " ")
	cleaned = strings.TrimSpace(cleaned)
	return cleaned, refs
}

func buildLinkButtons(refs []linkReference) []discordgo.MessageComponent {
	if len(refs) == 0 {
		return nil
	}

	limit := len(refs)
	if limit > maxLinkButtons {
		limit = maxLinkButtons
	}

	var components []discordgo.MessageComponent
	var currentRow []discordgo.MessageComponent

	for i := 0; i < limit; i++ {
		ref := refs[i]
		label := fmt.Sprintf("Source #%d", ref.Index)
		if ref.Display != "" {
			label = fmt.Sprintf("%s • %s", label, ref.Display)
		}
		button := discordgo.Button{
			Label: truncateForDiscord(label, maxButtonLabelRune),
			Style: discordgo.LinkButton,
			URL:   ref.URL,
		}
		currentRow = append(currentRow, button)
		if len(currentRow) == 5 {
			components = append(components, discordgo.ActionsRow{Components: currentRow})
			currentRow = nil
		}
	}

	if len(currentRow) > 0 {
		components = append(components, discordgo.ActionsRow{Components: currentRow})
	}

	return components
}

// BuildLinkButtons exposes link button construction for external callers.
func BuildLinkButtons(refs []linkReference) []discordgo.MessageComponent {
	return buildLinkButtons(refs)
}

func formatSimpleBlock(title string, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		body = "_No content_"
	}

	var sb strings.Builder
	sb.WriteString("```\n")
	if trimmedTitle := strings.TrimSpace(title); trimmedTitle != "" {
		sb.WriteString(trimmedTitle)
		sb.WriteString("\n\n")
	}
	sb.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("```")
	return sb.String()
}

func truncateForDiscord(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}

func summarizeURLDisplay(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}

	host := strings.TrimPrefix(parsed.Hostname(), "www.")
	path := strings.Trim(parsed.EscapedPath(), "/")
	if path == "" {
		return host
	}

	segments := strings.Split(path, "/")
	if len(segments) > 0 && segments[0] != "" {
		return fmt.Sprintf("%s/%s", host, segments[0])
	}
	return host
}
