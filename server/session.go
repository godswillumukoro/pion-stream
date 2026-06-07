// Command pion-stream — session.go manages the shared streaming state
// between the WHIP publisher and all WHEP viewers. It tracks whether a
// stream is live, the current viewer count, and relays media tracks from
// the publisher to every connected viewer.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/pion/webrtc/v4"
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

	// publisherPC is the WebRTC PeerConnection for the active
	// WHIP publisher. nil when no publisher is connected.
	publisherPC *webrtc.PeerConnection

	// publisherTracks stores the media tracks received from the
	// publisher. Keyed by track ID for lookup when relaying to viewers.
	publisherTracks map[string]*webrtc.TrackRemote

	// viewers is the list of active viewer PeerConnections.
	viewers []*webrtc.PeerConnection
}

// NewSession creates a new streaming session with the given
// configuration and logger.
func NewSession(cfg Config, logger zerolog.Logger) *Session {
	return &Session{
		config:          cfg,
		logger:          logger,
		publisherTracks: make(map[string]*webrtc.TrackRemote),
	}
}

// isLive returns true when a publisher is currently connected.
// Must be called while holding s.mu.
func (s *Session) isLive() bool {
	return s.publisherPC != nil
}

// viewerCount returns the number of currently connected viewers.
// Must be called while holding s.mu.
func (s *Session) numViewers() int {
	return len(s.viewers)
}

// HandleStatus returns the current stream status. When called via HTMX
// (HX-Request header present), it returns an HTML fragment that updates
// the status badge and viewer count in-place. Otherwise it returns JSON
// for programmatic consumers.
func (s *Session) HandleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	live := s.isLive()
	viewers := s.numViewers()
	s.mu.Unlock()

	// HTMX requests receive HTML fragments so the UI updates
	// without JavaScript or page reloads.
	if r.Header.Get("HX-Request") == "true" {
		s.writeStatusHTML(w, live, viewers)
		return
	}

	// Programmatic consumers receive JSON.
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(struct {
		Live    bool `json:"live"`
		Viewers int  `json:"viewers"`
	}{Live: live, Viewers: viewers}); err != nil {
		s.logger.Error().Err(err).Msg("failed to encode status response")
	}
}

// writeStatusHTML writes an HTML fragment containing the live/offline
// badge, viewer count, and footer status. The badge is the primary
// swapped element; viewer count and footer use hx-swap-oob for
// simultaneous in-place updates from a single HTMX request.
func (s *Session) writeStatusHTML(w http.ResponseWriter, live bool, viewers int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if live {
		s.writeHTML(w,
			`<span id="status-badge" class="badge live" hx-get="/status" hx-trigger="every 5s" hx-swap="outerHTML">`,
			`<span class="badge-dot"></span>`,
			`<span class="badge-label live">LIVE</span>`,
			`</span>`,
			`<span id="viewer-count" class="viewers" hx-swap-oob="true">`,
			fmtViewerCount(viewers),
			`</span>`,
			`<span id="footer-status" class="footer-item" hx-swap-oob="true">`,
			`<span class="footer-label">UPLINK</span>`,
			`<span style="color:var(--accent)">CONNECTED</span>`,
			`</span>`,
		)
	} else {
		s.writeHTML(w,
			`<span id="status-badge" class="badge" hx-get="/status" hx-trigger="every 5s" hx-swap="outerHTML">`,
			`<span class="badge-dot"></span>`,
			`<span class="badge-label offline">OFFLINE</span>`,
			`</span>`,
			`<span id="viewer-count" class="viewers" hx-swap-oob="true">`,
			`&mdash;`,
			`</span>`,
			`<span id="footer-status" class="footer-item" hx-swap-oob="true">`,
			`<span class="footer-label">UPLINK</span>`,
			`<span>NO CARRIER</span>`,
			`</span>`,
		)
	}
}

// writeHTML concatenates string fragments into the response writer.
func (s *Session) writeHTML(w http.ResponseWriter, parts ...string) {
	for _, part := range parts {
		if _, err := w.Write([]byte(part)); err != nil {
			s.logger.Error().Err(err).Msg("failed to write HTML fragment")
			return
		}
	}
}

// fmtViewerCount formats the viewer count for display.
func fmtViewerCount(n int) string {
	if n == 1 {
		return "1 viewer"
	}
	return fmt.Sprintf("%d viewers", n)
}

// HandleViewer serves the HTMX-powered viewer UI at the root path.
// The HTML template is embedded in the binary via go:embed.
func (s *Session) HandleViewer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(viewerTemplate); err != nil {
		s.logger.Error().Err(err).Msg("failed to write viewer template")
	}
}
