package discord

import (
    "fmt"
    "strings"
)

const (
    MaxDiscordMessageLen = 2000
    SafeChunkLen         = 1900
)

// BuildLongMessages formats a long message for Discord by chunking across messages.
func BuildLongMessages(message string, userID string) []string {
    firstMessage := fmt.Sprintf("<@%s> %s", userID, message)
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
    firstMaxLength := SafeChunkLen - len(fmt.Sprintf("<@%s> ", userID))
    paragraphs := strings.Split(message, "\n\n")

    var currentMessage strings.Builder
    isFirst := true

    for _, paragraph := range paragraphs {
        if len(paragraph) > SafeChunkLen {
            if currentMessage.Len() > 0 {
                if isFirst {
                    messages = append(messages, fmt.Sprintf("<@%s> %s", userID, currentMessage.String()))
                    isFirst = false
                } else {
                    messages = append(messages, currentMessage.String())
                }
                currentMessage.Reset()
            }

            sentences := splitBySentences(paragraph)
            for _, sentence := range sentences {
                effectiveMaxLength := SafeChunkLen
                if isFirst { effectiveMaxLength = firstMaxLength }
                if currentMessage.Len()+len(sentence)+2 > effectiveMaxLength {
                    if currentMessage.Len() > 0 {
                        if isFirst {
                            messages = append(messages, fmt.Sprintf("<@%s> %s", userID, currentMessage.String()))
                            isFirst = false
                        } else {
                            messages = append(messages, currentMessage.String())
                        }
                        currentMessage.Reset()
                    }
                }
                if currentMessage.Len() > 0 { currentMessage.WriteString(" ") }
                currentMessage.WriteString(sentence)
            }
        } else {
            effectiveMaxLength := SafeChunkLen
            if isFirst { effectiveMaxLength = firstMaxLength }
            if currentMessage.Len()+len(paragraph)+4 > effectiveMaxLength {
                if currentMessage.Len() > 0 {
                    if isFirst {
                        messages = append(messages, fmt.Sprintf("<@%s> %s", userID, currentMessage.String()))
                        isFirst = false
                    } else {
                        messages = append(messages, currentMessage.String())
                    }
                    currentMessage.Reset()
                }
            }
            if currentMessage.Len() > 0 { currentMessage.WriteString("\n\n") }
            currentMessage.WriteString(paragraph)
        }
    }

    if currentMessage.Len() > 0 {
        if isFirst {
            messages = append(messages, fmt.Sprintf("<@%s> %s", userID, currentMessage.String()))
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
            if chunk.Len() > 0 { chunk.WriteString(" ") }
            chunk.WriteString(word)
        }
        if chunk.Len() > 0 {
            chunks = append(chunks, chunk.String())
        }
        return chunks
    }
    return sentences
}


