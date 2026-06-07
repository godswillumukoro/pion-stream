// Package main — session.go manages the shared streaming state between
// the WHIP publisher and all WHEP viewers. It tracks whether a stream
// is live, the current viewer count, and relays media tracks from the
// publisher to every connected viewer.
package main

import (
	"net/http"
	"sync"

	"github.com/rs/zerolog"
)

// Session holds the global state for a single active stream.
// One publisher can be connected at a time. Multiple viewers
// can connect simultaneously — each receives a copy of the
// publisher's media tracks.
type Session struct {
	config Config
	logger zerolog.Logger

	mu sync.Mutex

	// live is true when a WHIP publisher is currently connected.
	live bool

	// viewerCount tracks the number of active WHEP viewer connections.
	viewerCount int
}

// NewSession creates a new streaming session with the given
// configuration and logger.
func NewSession(cfg Config, logger zerolog.Logger) *Session {
	return &Session{
		config: cfg,
		logger: logger,
	}
}

// HandleWHIP is a placeholder for the WHIP ingest handler.
func (s *Session) HandleWHIP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// HandleWHIPDisconnect is a placeholder for the WHIP disconnect handler.
func (s *Session) HandleWHIPDisconnect(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// HandleWHEP is a placeholder for the WHEP egress handler.
func (s *Session) HandleWHEP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// HandleStatus returns the current stream status as JSON.
func (s *Session) HandleStatus(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// HandleViewer serves the HTMX-powered viewer UI.
func (s *Session) HandleViewer(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
