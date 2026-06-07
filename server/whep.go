// Command pion-stream — whep.go handles the WHEP (WebRTC-HTTP Egress
// Protocol) endpoint. Browser-based viewers POST their SDP offer to
// receive the stream. The server creates a PeerConnection, relays the
// publisher's media tracks, and returns an SDP answer.
package main

import (
	"io"
	"net/http"

	"github.com/pion/webrtc/v4"
)

// HandleWHEP processes a WHEP egress request. The viewer (browser)
// sends an SDP offer in the request body. The server creates a
// PeerConnection and adds copies of all active publisher tracks.
// It then returns an SDP answer so the browser can begin playback.
func (s *Session) HandleWHEP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if !s.isLive() {
		s.mu.Unlock()
		http.Error(w, "no active stream", http.StatusServiceUnavailable)
		s.logger.Warn().Msg("whep: viewer requested but no publisher active")
		return
	}

	// Copy the publisher's tracks while holding the lock so we
	// can set up the relay outside the critical section.
	publisherPC := s.publisherPC
	tracks := make(map[string]*webrtc.TrackRemote, len(s.publisherTracks))
	for id, track := range s.publisherTracks {
		tracks[id] = track
	}
	s.mu.Unlock()

	// Read the viewer's SDP offer.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		s.logger.Error().Err(err).Msg("whep: failed to read request body")
		return
	}
	defer r.Body.Close()

	offerStr := string(body)
	if offerStr == "" {
		http.Error(w, "empty SDP offer", http.StatusBadRequest)
		s.logger.Warn().Msg("whep: empty SDP offer received")
		return
	}

	s.logger.Info().
		Int("offer_bytes", len(offerStr)).
		Int("track_count", len(tracks)).
		Msg("whep: received viewer offer")

	// Use the same ICE configuration as the publisher for consistency.
	api := s.newAPI()

	viewerPC, err := api.NewPeerConnection(webrtc.Configuration{
		ICEServers: s.iceServers(),
	})
	if err != nil {
		http.Error(w, "failed to create peer connection", http.StatusInternalServerError)
		s.logger.Error().Err(err).Msg("whep: failed to create viewer peer connection")
		return
	}

	// Relay publisher tracks to this viewer. For each remote track
	// from the publisher, we create a local track and forward RTP
	// packets in a background goroutine.
	for _, remoteTrack := range tracks {
		if err := s.relayTrack(viewerPC, remoteTrack, publisherPC); err != nil {
			s.logger.Error().Err(err).
				Str("track_id", remoteTrack.ID()).
				Msg("whep: failed to relay track")
		}
	}

	// Track viewer connection state.
	viewerID := generateViewerID()
	s.addViewer(viewerPC)

	viewerPC.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		s.logger.Info().
			Str("viewer_id", viewerID).
			Str("state", state.String()).
			Msg("whep: viewer ICE connection state changed")

		if state == webrtc.ICEConnectionStateDisconnected ||
			state == webrtc.ICEConnectionStateFailed ||
			state == webrtc.ICEConnectionStateClosed {
			s.removeViewer(viewerPC)
			s.logger.Info().
				Str("viewer_id", viewerID).
				Msg("whep: viewer disconnected")
		}
	})

	// Set the remote description (the viewer's SDP offer).
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offerStr,
	}

	if err := viewerPC.SetRemoteDescription(offer); err != nil {
		http.Error(w, "failed to set remote description", http.StatusBadRequest)
		s.logger.Error().Err(err).Msg("whep: failed to set viewer remote description")
		s.removeViewer(viewerPC)
		_ = viewerPC.Close()
		return
	}

	// Create an SDP answer.
	answer, err := viewerPC.CreateAnswer(nil)
	if err != nil {
		http.Error(w, "failed to create answer", http.StatusInternalServerError)
		s.logger.Error().Err(err).Msg("whep: failed to create viewer answer")
		s.removeViewer(viewerPC)
		_ = viewerPC.Close()
		return
	}

	if err := viewerPC.SetLocalDescription(answer); err != nil {
		http.Error(w, "failed to set local description", http.StatusInternalServerError)
		s.logger.Error().Err(err).Msg("whep: failed to set viewer local description")
		s.removeViewer(viewerPC)
		_ = viewerPC.Close()
		return
	}

	// Return the SDP answer.
	w.Header().Set("Content-Type", "application/sdp")
	w.WriteHeader(http.StatusCreated)
	if _, err := w.Write([]byte(answer.SDP)); err != nil {
		s.logger.Error().Err(err).Msg("whep: failed to write answer SDP")
	}

	s.logger.Info().
		Str("viewer_id", viewerID).
		Int("answer_bytes", len(answer.SDP)).
		Msg("whep: viewer connected successfully")
}

// relayTrack creates a local track on the viewer's PeerConnection that
// mirrors the publisher's remote track. RTP packets are read from the
// publisher and written to the viewer in a background goroutine that
// stops when the viewer disconnects.
func (s *Session) relayTrack(
	viewerPC *webrtc.PeerConnection,
	publisherTrack *webrtc.TrackRemote,
	publisherPC *webrtc.PeerConnection,
) error {
	// Create a local track with the same codec as the publisher.
	localTrack, err := webrtc.NewTrackLocalStaticRTP(
		publisherTrack.Codec().RTPCodecCapability,
		publisherTrack.ID(),
		publisherTrack.StreamID(),
	)
	if err != nil {
		return err
	}

	// Add the local track to the viewer's peer connection.
	if _, err := viewerPC.AddTrack(localTrack); err != nil {
		return err
	}

	// Read RTP packets from the publisher and forward them to the viewer.
	go func() {
		defer func() {
			s.logger.Info().
				Str("track_id", publisherTrack.ID()).
				Msg("whep: track relay stopped")
		}()

		buf := make([]byte, 1500) // Standard MTU-sized buffer for RTP packets.
		for {
			n, _, readErr := publisherTrack.Read(buf)
			if readErr != nil {
				// Track closed — publisher disconnected or stream ended.
				return
			}

			if _, writeErr := localTrack.Write(buf[:n]); writeErr != nil {
				// Viewer disconnected.
				return
			}
		}
	}()

	return nil
}

// relayTrack creates a local track on the viewer's PeerConnection that