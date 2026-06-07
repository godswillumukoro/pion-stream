// Command pion-stream — session.go manages the shared streaming state
// between the WHIP publisher and all WHEP viewers. It tracks whether a
// stream is live, the current viewer count, and relays media tracks from
// the publisher to every connected viewer.
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/pion/ice/v4"
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

	// udpMux is the shared ICE UDP mux that allows all peer
	// connections to share a single UDP port.
	udpMux ice.UDPMux

	// publisherPC is the WebRTC PeerConnection for the active
	// WHIP publisher. nil when no publisher is connected.
	publisherPC *webrtc.PeerConnection

	// publisherType identifies the source: "obs" or "browser".
	// Empty string when no publisher is connected.
	publisherType string

	// startedAt records when the current publisher went live.
	// Zero time when no publisher is connected.
	startedAt time.Time

	// publisherTracks stores the media tracks received from the
	// publisher. Keyed by track ID for lookup when relaying to viewers.
	publisherTracks map[string]*webrtc.TrackRemote

	// viewers is the list of active viewer PeerConnections.
	viewers []*webrtc.PeerConnection
}

// NewSession creates a new streaming session with the given
// configuration and logger. It sets up a shared ICE UDP mux
// on the configured port so all WebRTC connections share a
// single UDP socket.
func NewSession(cfg Config, logger zerolog.Logger) *Session {
	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{
		IP:   net.IPv4zero,
		Port: cfg.UDPMuxPort,
	})
	if err != nil {
		logger.Fatal().Err(err).Int("port", cfg.UDPMuxPort).
			Msg("failed to create UDP listener")
	}

	udpMux := webrtc.NewICEUDPMux(nil, udpListener)

	logger.Info().Int("port", cfg.UDPMuxPort).Msg("ICE UDP mux started")

	return &Session{
		config:          cfg,
		logger:          logger,
		udpMux:          udpMux,
		publisherTracks: make(map[string]*webrtc.TrackRemote),
	}
}

// setPublisher records the publisher's peer connection, type,
// and start time. Must be called with s.mu held.
func (s *Session) setPublisher(pc *webrtc.PeerConnection, pubType string) {
	s.publisherPC = pc
	s.publisherType = pubType
	s.startedAt = time.Now()
}

// clearPublisher removes the current publisher and resets state.
// Must be called with s.mu held.
func (s *Session) clearPublisher() {
	s.publisherPC = nil
	s.publisherType = ""
	s.startedAt = time.Time{}
	s.publisherTracks = make(map[string]*webrtc.TrackRemote)
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
	pubType := s.publisherType
	durationSec := int64(0)
	if live {
		durationSec = int64(time.Since(s.startedAt).Seconds())
	}
	s.mu.Unlock()

	// HTMX requests receive HTML fragments so the UI updates
	// without JavaScript or page reloads.
	if r.Header.Get("HX-Request") == "true" {
		s.writeStatusHTML(w, live, viewers, pubType)
		return
	}

	// Programmatic consumers receive JSON.
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(struct {
		Live            bool   `json:"live"`
		PublisherType   string `json:"publisher_type"`
		Viewers         int    `json:"viewers"`
		DurationSeconds int64  `json:"duration_seconds"`
	}{Live: live, PublisherType: pubType, Viewers: viewers,
		DurationSeconds: durationSec}); err != nil {
		s.logger.Error().Err(err).Msg("failed to encode status response")
	}
}

// writeStatusHTML writes an HTML fragment containing the live/offline
// badge, viewer count, and footer status. The badge is the primary
// swapped element; viewer count and footer use hx-swap-oob for
// simultaneous in-place updates from a single HTMX request.
func (s *Session) writeStatusHTML(w http.ResponseWriter, live bool, viewers int, pubType string) {
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

// HandleSite serves the companion landing page — a standalone
// documentation site for the project.
func (s *Session) HandleSite(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(siteTemplate); err != nil {
		s.logger.Error().Err(err).Msg("failed to write site template")
	}
}

// HandleStudio serves the browser studio — a full-featured
// broadcasting interface for publishing directly from the browser.
func (s *Session) HandleStudio(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(studioTemplate); err != nil {
		s.logger.Error().Err(err).Msg("failed to write studio template")
	}
}

// HandleViewer serves the HTMX-powered viewer UI at the root path.
// The HTML template is embedded in the binary via go:embed.
func (s *Session) HandleViewer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(viewerTemplate); err != nil {
		s.logger.Error().Err(err).Msg("failed to write viewer template")
	}
}

// addViewer registers a new viewer PeerConnection and increments the
// viewer count. Must be called with s.mu held.
func (s *Session) addViewer(pc *webrtc.PeerConnection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.viewers = append(s.viewers, pc)
}

// removeViewer unregisters a viewer PeerConnection and decrements the
// viewer count. Safe to call even if the viewer isn't in the list.
func (s *Session) removeViewer(pc *webrtc.PeerConnection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, v := range s.viewers {
		if v == pc {
			s.viewers = append(s.viewers[:i], s.viewers[i+1:]...)
			return
		}
	}
}

// viewerIDCounter is a simple incrementing counter for generating
// human-readable viewer IDs in log output.
var viewerIDCounter int

// generateViewerID returns a unique viewer identifier for logging.
func generateViewerID() string {
	viewerIDCounter++
	return fmt.Sprintf("viewer-%d", viewerIDCounter)
}

// newAPI creates a configured WebRTC API with the shared ICE UDP
// mux and NAT 1:1 IP mapping. This is used by both WHIP and WHEP
// handlers so all peer connections share one UDP socket.
func (s *Session) newAPI() *webrtc.API {
	settingEngine := webrtc.SettingEngine{}
	settingEngine.SetICEUDPMux(s.udpMux)

	if s.config.PublicIP != "" {
		settingEngine.SetNAT1To1IPs(
			[]string{s.config.PublicIP},
			webrtc.ICECandidateTypeHost,
		)
	}

	return webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))
}
