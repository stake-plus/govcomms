package discord

import (
	"fmt"
	"regexp"
	"strings"
)

var urlNoEmbedRegex = regexp.MustCompile(`https?://[^\s\[\]()<>]+`)

// WrapURLsNoEmbed wraps URLs in angle brackets to prevent Discord embeds.
func WrapURLsNoEmbed(text string) string {
	if text == "" {
		return text
	}

	matches := urlNoEmbedRegex.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return text
	}

	var builder strings.Builder
	builder.Grow(len(text) + len(matches)*2)

	last := 0
	for _, match := range matches {
		start, end := match[0], match[1]
		builder.WriteString(text[last:start])

		if isAlreadyAngleWrapped(text, start, end) {
			builder.WriteString(text[start:end])
		} else {
			core, punctuation := trimTrailingPunctuation(text[start:end])
			if core == "" {
				builder.WriteString(text[start:end])
			} else {
				builder.WriteString("<")
				builder.WriteString(core)
				builder.WriteString(">")
				builder.WriteString(punctuation)
			}
		}

		last = end
	}

	builder.WriteString(text[last:])
	return builder.String()
}

// FormatURLsNoEmbed formats a slice of URLs, wrapped to prevent embeds.
func FormatURLsNoEmbed(urls []string) string {
	if len(urls) == 0 {
		return ""
	}
	var formatted []string
	for _, u := range urls {
		formatted = append(formatted, fmt.Sprintf("<%s>", u))
	}
	return strings.Join(formatted, " ")
}

// FormatURLsNoEmbedMultiline formats URLs (no embeds) separated by newlines to keep buttons tidy.
func FormatURLsNoEmbedMultiline(urls []string) string {
	if len(urls) == 0 {
		return ""
	}
	var lines []string
	for _, u := range urls {
		trimmed := strings.TrimSpace(u)
		if trimmed == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("<%s>", trimmed))
	}
	return strings.Join(lines, "\n")
}

func isAlreadyAngleWrapped(text string, start, end int) bool {
	return start > 0 && text[start-1] == '<' && end < len(text) && text[end] == '>'
}

func trimTrailingPunctuation(input string) (string, string) {
	if input == "" {
		return "", ""
	}

	runes := []rune(input)
	idx := len(runes)

	for idx > 0 {
		switch runes[idx-1] {
		case '.', ',', ';', ':', '!', '?', ')':
			idx--
		default:
			return string(runes[:idx]), string(runes[idx:])
		}
	}

	return "", input
}
