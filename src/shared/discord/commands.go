package discord

import (
    "strings"
)

// Router is a minimal command router for Discord message content.
type Router struct {
    handlers map[string]func()
}

func NewRouter() *Router {
    return &Router{handlers: make(map[string]func())}
}

func (r *Router) On(prefix string, handler func()) {
    r.handlers[prefix] = handler
}

// Match runs the first handler whose prefix matches the message.
func (r *Router) Match(content string) bool {
    content = strings.TrimSpace(content)
    for prefix, h := range r.handlers {
        if strings.HasPrefix(content, prefix) {
            h()
            return true
        }
    }
    return false
}


