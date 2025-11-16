package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stake-plus/govcomms/src/cache"
)

// Config controls the MCP server runtime.
type Config struct {
	ListenAddr string
	AuthToken  string
	Logger     *log.Logger
}

// Server exposes referendum cache data via a lightweight MCP-inspired API.
type Server struct {
	cache      *cache.Manager
	cfg        Config
	httpServer *http.Server
}

// NewServer constructs a server bound to the provided cache manager.
func NewServer(cfg Config, cacheManager *cache.Manager) (*Server, error) {
	if cacheManager == nil {
		return nil, fmt.Errorf("mcp: cache manager is required")
	}
	if strings.TrimSpace(cfg.ListenAddr) == "" {
		cfg.ListenAddr = "127.0.0.1:7081"
	}
	return &Server{
		cache: cacheManager,
		cfg:   cfg,
	}, nil
}

// Start begins serving requests until the context is cancelled or Stop is called.
func (s *Server) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("mcp: listen %s: %w", s.cfg.ListenAddr, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.wrapAuth(s.handleHealth))
	mux.HandleFunc("/v1/referenda/", s.wrapAuth(s.handleReferenda))

	s.httpServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	s.logf("listening on %s", s.cfg.ListenAddr)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.httpServer.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpServer.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) wrapAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token := strings.TrimSpace(s.cfg.AuthToken); token != "" {
			auth := strings.TrimSpace(r.Header.Get("Authorization"))
			if !strings.HasPrefix(auth, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")) != token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReferenda(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/v1/referenda/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 {
		http.Error(w, "missing network or refId", http.StatusBadRequest)
		return
	}

	network := parts[0]
	refID, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		http.Error(w, "invalid refId", http.StatusBadRequest)
		return
	}

	segment := ""
	if len(parts) > 2 {
		segment = parts[2]
	}

	switch segment {
	case "", "metadata":
		s.handleMetadata(w, network, uint32(refID))
	case "content":
		s.handleContent(w, network, uint32(refID))
	case "attachments":
		s.handleAttachments(w, network, uint32(refID))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleMetadata(w http.ResponseWriter, network string, refID uint32) {
	entry, err := s.cache.EnsureEntry(network, refID)
	if err != nil {
		http.Error(w, fmt.Sprintf("cache load failed: %v", err), http.StatusInternalServerError)
		return
	}
	payload := ReferendumPayload{
		Network:     entry.Network,
		RefID:       entry.RefID,
		Attachments: entry.Attachments,
		RefreshedAt: entry.RefreshedAt,
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleContent(w http.ResponseWriter, network string, refID uint32) {
	content, err := s.cache.GetProposalContent(network, refID)
	if err != nil {
		http.Error(w, fmt.Sprintf("read content failed: %v", err), http.StatusInternalServerError)
		return
	}
	entry, _ := s.cache.EnsureEntry(network, refID)
	payload := ReferendumPayload{
		Network:     strings.TrimSpace(network),
		RefID:       refID,
		Content:     content,
		Attachments: entry.Attachments,
		RefreshedAt: entry.RefreshedAt,
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleAttachments(w http.ResponseWriter, network string, refID uint32) {
	entry, err := s.cache.EnsureEntry(network, refID)
	if err != nil {
		http.Error(w, fmt.Sprintf("cache load failed: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"network":     entry.Network,
		"refId":       entry.RefID,
		"attachments": entry.Attachments,
		"refreshedAt": entry.RefreshedAt,
	})
}

func (s *Server) logf(format string, args ...any) {
	if s.cfg.Logger == nil {
		return
	}
	s.cfg.Logger.Printf(format, args...)
}

// ReferendumPayload structures the MCP response bodies.
type ReferendumPayload struct {
	Network     string             `json:"network"`
	RefID       uint32             `json:"refId"`
	Content     string             `json:"content,omitempty"`
	Attachments []cache.Attachment `json:"attachments,omitempty"`
	RefreshedAt time.Time          `json:"refreshedAt"`
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}
