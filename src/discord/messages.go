package discord

import (
	"fmt"
	"regexp"
	"strings"
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

// BuildStyledMessages formats content with a consistent header + blockquote aesthetic
// and chunks it into Discord-safe messages.
func BuildStyledMessages(title string, body string, userID string) []string {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}

	cleaned := BeautifyForDiscord(body)
	chunks := splitMessage(cleaned, "")
	if len(chunks) == 0 {
		chunks = []string{cleaned}
	}

	var messages []string
	for idx, chunk := range chunks {
		currentTitle := title
		if idx > 0 {
			currentTitle = ""
		}
		content := FormatStyledBlock(currentTitle, chunk)

		if len(chunks) > 1 {
			if idx < len(chunks)-1 {
				content += "\n*(continued...)*"
			} else {
				content += "\n*(end of response)*"
			}
		}

		if idx == 0 && userID != "" {
			content = fmt.Sprintf("<@%s>\n%s", userID, content)
		}

		messages = append(messages, content)
	}

	return messages
}

// FormatStyledBlock returns a single styled block suitable for shorter Discord messages.
func FormatStyledBlock(title string, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		body = "_No content_"
	}
	body = BeautifyForDiscord(body)

	var sb strings.Builder
	if strings.TrimSpace(title) != "" {
		sb.WriteString(fmt.Sprintf("**%s**\n%s\n", title, dividerForTitle(title)))
	}
	sb.WriteString(styleBlockquote(body))
	return sb.String()
}

func dividerForTitle(title string) string {
	clean := strings.TrimSpace(title)
	clean = strings.Trim(clean, "*_` ")
	length := len([]rune(clean))
	if length < 6 {
		length = 6
	}
	return strings.Repeat("─", length+2)
}

func styleBlockquote(text string) string {
	if text == "" {
		return "> "
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + trimmed
		}
	}
	return strings.Join(lines, "\n")
}
