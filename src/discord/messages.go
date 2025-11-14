package discord

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
)

const (
	MaxDiscordMessageLen = 2000
	SafeChunkLen         = 1900
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

var newlineCollapse = regexp.MustCompile(`\n{3,}`)

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

// StyledMessage represents a fully formatted Discord message plus optional components.
type StyledMessage struct {
	Content    string
	Components []discordgo.MessageComponent
	BoxLines   []string
}

const (
	maxLinkButtons     = 25
	maxButtonLabelRune = 80

	boxInnerWidth    = 68 // ~15% narrower than previous layout
	boxPadding       = 1
	boxColumnsGap    = "  "
	boxLineWidth     = boxInnerWidth + (boxPadding * 2) + 4
	maxComponentRows = 5

	ansiDim   = "\u001b[2m"
	ansiReset = "\u001b[0m"
)

var wrappedURLRegex = regexp.MustCompile(`<https?://[^\s<>]+>`)

// BuildStyledMessages formats content with a consistent professional code-block style,
// splits it into Discord-safe chunks, and attaches link buttons when URLs are detected.
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

func buildStyledMessageFromCleanChunk(title string, cleanedBody string) StyledMessage {
	trimmed := strings.TrimSpace(cleanedBody)
	if trimmed == "" {
		trimmed = "_No content_"
	}

	withoutURLs, refs := replaceURLsWithReferences(trimmed)
	boxLines := renderProfessionalBox(title, withoutURLs)

	return StyledMessage{
		Content:    wrapBoxLines(boxLines),
		Components: buildLinkButtons(refs),
		BoxLines:   boxLines,
	}
}

func replaceURLsWithReferences(input string) (string, []linkReference) {
	matches := wrappedURLRegex.FindAllStringIndex(input, -1)
	if len(matches) == 0 {
		return input, nil
	}

	var builder strings.Builder
	builder.Grow(len(input) + len(matches)*8)

	refOrder := make([]linkReference, 0, len(matches))
	seen := make(map[string]int)
	last := 0

	for _, match := range matches {
		builder.WriteString(input[last:match[0]])

		raw := input[match[0]:match[1]]
		urlStr := strings.Trim(raw, "<>")

		idx, exists := seen[urlStr]
		if !exists {
			ref := linkReference{
				Index:   len(refOrder) + 1,
				URL:     urlStr,
				Display: summarizeURLDisplay(urlStr),
			}
			refOrder = append(refOrder, ref)
			idx = len(refOrder) - 1
			seen[urlStr] = idx
		}

		_ = refOrder[idx]
		builder.WriteString(fmt.Sprintf("[Source %d]", refOrder[idx].Index))
		last = match[1]
	}

	builder.WriteString(input[last:])
	return builder.String(), refOrder
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
		button := discordgo.Button{
			Label: truncateForDiscord(fmt.Sprintf("Source %d · %s", ref.Index, ref.Display), maxButtonLabelRune),
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

// CombineStyledGroup merges multiple StyledMessages into a single payload laid out horizontally.
func CombineStyledGroup(group []StyledMessage) StyledMessage {
	switch len(group) {
	case 0:
		return StyledMessage{}
	case 1:
		return group[0]
	}

	combinedLines := combineBoxLineSets(group)
	return StyledMessage{
		Content:    wrapBoxLines(combinedLines),
		Components: mergeComponentRows(group),
		BoxLines:   combinedLines,
	}
}

func renderProfessionalBox(title string, body string) []string {
	bodyLines := wrapBodyLines(body, boxInnerWidth)
	if len(bodyLines) == 0 {
		bodyLines = []string{""}
	}

	innerWidth := boxInnerWidth + boxPadding*2
	border := strings.Repeat("─", innerWidth+2)

	var lines []string
	lines = append(lines, "╭"+border+"╮")

	if trimmedTitle := strings.TrimSpace(title); trimmedTitle != "" {
		lines = append(lines, formatBoxLine(trimmedTitle, innerWidth))
		lines = append(lines, "├"+border+"┤")
	}

	for _, line := range bodyLines {
		lines = append(lines, formatBoxLine(line, innerWidth))
	}

	lines = append(lines, "╰"+border+"╯")
	return lines
}

func wrapBoxLines(lines []string) string {
	if len(lines) == 0 {
		return "```ansi\n```"
	}
	content := strings.Join(lines, "\n")
	return fmt.Sprintf("```ansi\n%s%s%s\n```", ansiDim, content, ansiReset)
}

func formatBoxLine(content string, innerWidth int) string {
	padded := padRight(content, boxInnerWidth)
	leftPad := strings.Repeat(" ", boxPadding)
	rightPad := leftPad
	return fmt.Sprintf("│ %s%s%s │", leftPad, padded, rightPad)
}

func wrapBodyLines(body string, width int) []string {
	raw := strings.Split(body, "\n")
	var lines []string
	for _, line := range raw {
		line = strings.TrimRight(line, " ")
		if line == "" {
			lines = append(lines, "")
			continue
		}
		wrapped := wrapLine(line, width)
		lines = append(lines, wrapped...)
	}
	return lines
}

func wrapLine(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}

	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{""}
	}

	var temp []string
	var current strings.Builder

	for _, word := range words {
		if current.Len() == 0 {
			current.WriteString(word)
			continue
		}

		if runeLen(current.String())+1+runeLen(word) > width {
			temp = append(temp, current.String())
			current.Reset()
			current.WriteString(word)
		} else {
			current.WriteByte(' ')
			current.WriteString(word)
		}
	}

	if current.Len() > 0 {
		temp = append(temp, current.String())
	}

	var lines []string
	for _, entry := range temp {
		if runeLen(entry) <= width {
			lines = append(lines, entry)
			continue
		}
		lines = append(lines, splitLongWord(entry, width)...)
	}

	return lines
}

func splitLongWord(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	var result []string
	runes := []rune(text)
	for len(runes) > width {
		result = append(result, string(runes[:width]))
		runes = runes[width:]
	}
	if len(runes) > 0 {
		result = append(result, string(runes))
	}
	return result
}

func padRight(text string, width int) string {
	runes := []rune(text)
	if len(runes) >= width {
		return string(runes[:width])
	}
	return text + strings.Repeat(" ", width-len(runes))
}

func runeLen(value string) int {
	return utf8.RuneCountInString(value)
}

func combineBoxLineSets(group []StyledMessage) []string {
	maxLines := 0
	lineWidths := make([]int, len(group))
	for idx, msg := range group {
		if len(msg.BoxLines) > maxLines {
			maxLines = len(msg.BoxLines)
		}
		if len(msg.BoxLines) > 0 {
			lineWidths[idx] = len(msg.BoxLines[0])
		} else {
			lineWidths[idx] = boxLineWidth
		}
	}
	if maxLines == 0 {
		return nil
	}

	var combined []string
	for lineIdx := 0; lineIdx < maxLines; lineIdx++ {
		var segments []string
		for colIdx, msg := range group {
			if lineIdx < len(msg.BoxLines) {
				segments = append(segments, msg.BoxLines[lineIdx])
			} else {
				segments = append(segments, strings.Repeat(" ", lineWidths[colIdx]))
			}
		}
		combined = append(combined, strings.Join(segments, boxColumnsGap))
	}
	return combined
}

func mergeComponentRows(group []StyledMessage) []discordgo.MessageComponent {
	var merged []discordgo.MessageComponent
	for _, msg := range group {
		if len(msg.Components) == 0 {
			continue
		}
		for _, component := range msg.Components {
			if len(merged) >= maxComponentRows {
				return merged
			}
			merged = append(merged, component)
		}
	}
	return merged
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
