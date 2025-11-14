package discord

import (
    "fmt"
    "regexp"
    "strings"
)

// WrapURLsNoEmbed wraps URLs in angle brackets to prevent Discord embeds.
func WrapURLsNoEmbed(text string) string {
    urlRegex := regexp.MustCompile(`https?://[^\s\[\]()<>]+`)
    return urlRegex.ReplaceAllStringFunc(text, func(url string) string {
        url = strings.TrimRight(url, ".,;:!?)")
        if strings.HasPrefix(url, "<") && strings.HasSuffix(url, ">") {
            return url
        }
        return fmt.Sprintf("<%s>", url)
    })
}

// FormatURLsNoEmbed formats a slice of URLs, wrapped to prevent embeds.
func FormatURLsNoEmbed(urls []string) string {
    if len(urls) == 0 { return "" }
    var formatted []string
    for _, u := range urls { formatted = append(formatted, fmt.Sprintf("<%s>", u)) }
    return strings.Join(formatted, " ")
}


