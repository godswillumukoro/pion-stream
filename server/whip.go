// Command pion-stream — whip.go handles the WHIP (WebRTC-HTTP Ingest
// Protocol) endpoints. Both OBS Studio and browser-based publishers
// use these endpoints to stream to the server.
package main

import (
	"io"
	"net/http"
	"strings"

	"github.com/pion/webrtc/v4"
)

// handleWHIPInternal processes a WHIP ingest request for any publisher
// type. It handles authentication, SDP offer/answer negotiation, and
// track storage. The pubType parameter identifies the source ("obs" or
// "browser") and is used for logging and status reporting.
func (s *Session) handleWHIPInternal(
	w http.ResponseWriter, r *http.Request, pubType string,
) {
	// Authenticate if a stream key is configured.
	if s.config.StreamKey != "" {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") ||
			strings.TrimPrefix(auth, "Bearer ") != s.config.StreamKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			s.logger.Warn().Str("type", pubType).
				Msg("whip: rejected — invalid stream key")
			return
		}
	}

	// Read the SDP offer body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		s.logger.Error().Err(err).Str("type", pubType).
			Msg("whip: failed to read request body")
		return
	}
	defer r.Body.Close()

	offerStr := string(body)
	if offerStr == "" {
		http.Error(w, "empty SDP offer", http.StatusBadRequest)
		s.logger.Warn().Str("type", pubType).
			Msg("whip: empty SDP offer received")
		return
	}

	s.logger.Info().
		Str("type", pubType).
		Int("offer_bytes", len(offerStr)).
		Msg("whip: received publish offer")

	// Prevent duplicate publishers — disconnect any existing one.
	s.mu.Lock()
	if s.publisherPC != nil {
		s.logger.Warn().
			Str("previous_type", s.publisherType).
			Msg("whip: disconnecting existing publisher")
		_ = s.publisherPC.Close()
		s.clearPublisher()
	}
	s.mu.Unlock()

	// Create the WebRTC API with the shared ICE UDP mux.
	api := s.newAPI()

	peerConnection, err := api.NewPeerConnection(webrtc.Configuration{
		ICEServers: s.iceServers(),
	})
	if err != nil {
		http.Error(w, "failed to create peer connection",
			http.StatusInternalServerError)
		s.logger.Error().Err(err).Str("type", pubType).
			Msg("whip: failed to create peer connection")
		return
	}

	// When the publisher sends media tracks, store them and
	// start a broadcast writer goroutine that fans out RTP
	// packets to all connected WHEP viewers.
	peerConnection.OnTrack(func(
		track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver,
	) {
		s.logger.Info().
			Str("track_id", track.ID()).
			Str("kind", track.Kind().String()).
			Str("codec", track.Codec().MimeType).
			Str("type", pubType).
			Msg("whip: received track from publisher")

		pt := &publisherTrack{
			track: track,
			codec: track.Codec().RTPCodecCapability,
			kind:  track.Kind(),
		}

		s.mu.Lock()
		s.publisherTracks[track.ID()] = pt
		s.mu.Unlock()

		// Start the broadcast writer — one goroutine per track
		// that reads once and fans out to all viewers.
		s.startBroadcastWriter(pt)
	})

	// Handle ICE connection state changes for logging.
	peerConnection.OnICEConnectionStateChange(
		func(state webrtc.ICEConnectionState) {
			s.logger.Info().
				Str("state", state.String()).
				Str("type", pubType).
				Msg("whip: ICE connection state changed")

			if state == webrtc.ICEConnectionStateDisconnected ||
				state == webrtc.ICEConnectionStateFailed ||
				state == webrtc.ICEConnectionStateClosed {
				s.mu.Lock()
				if s.publisherPC == peerConnection {
					s.clearPublisher()
					s.logger.Info().Str("type", pubType).
						Msg("whip: publisher disconnected")
				}
				s.mu.Unlock()
			}
		})

	// Set the remote description (the publisher's SDP offer).
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offerStr,
	}

	if err := peerConnection.SetRemoteDescription(offer); err != nil {
		http.Error(w, "failed to set remote description",
			http.StatusBadRequest)
		s.logger.Error().Err(err).Str("type", pubType).
			Msg("whip: failed to set remote description")
		_ = peerConnection.Close()
		return
	}

	// Create an SDP answer.
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		http.Error(w, "failed to create answer",
			http.StatusInternalServerError)
		s.logger.Error().Err(err).Str("type", pubType).
			Msg("whip: failed to create answer")
		_ = peerConnection.Close()
		return
	}

	// Set the local description (our answer).
	if err := peerConnection.SetLocalDescription(answer); err != nil {
		http.Error(w, "failed to set local description",
			http.StatusInternalServerError)
		s.logger.Error().Err(err).Str("type", pubType).
			Msg("whip: failed to set local description")
		_ = peerConnection.Close()
		return
	}

	// Wait for ICE candidate gathering to complete before
	// sending the answer. This ensures all candidates are
	// included and avoids the need for trickle ICE.
	<-webrtc.GatheringCompletePromise(peerConnection)

	// Store the publisher's peer connection, type, and start time.
	s.mu.Lock()
	s.setPublisher(peerConnection, pubType)
	s.mu.Unlock()

	// Return the SDP answer with WHIP-specific headers.
	// The Location header tells WHIP clients where to send DELETE.
	answerSDP := appendCandidateToAnswer(
		peerConnection.LocalDescription().SDP,
	)

	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("Location", "/api/whip")
	w.Header().Set("Access-Control-Expose-Headers", "Location, ETag")
	w.WriteHeader(http.StatusCreated)
	if _, err := w.Write([]byte(answerSDP)); err != nil {
		s.logger.Error().Err(err).Msg("whip: failed to write answer SDP")
	}

	s.logger.Info().
		Str("type", pubType).
		Int("answer_bytes", len(answerSDP)).
		Msg("whip: publisher connected successfully")
}

// HandleWHIP processes a WHIP ingest request from OBS Studio.
// POST /api/whip
func (s *Session) HandleWHIP(w http.ResponseWriter, r *http.Request) {
	s.handleWHIPInternal(w, r, "obs")
}

// HandleWHIPBrowser processes a WHIP ingest request from a browser
// publisher. Logically identical to HandleWHIP but uses a separate
// route for clarity in the video narration.
// POST /api/whip/browser
func (s *Session) HandleWHIPBrowser(w http.ResponseWriter, r *http.Request) {
	s.handleWHIPInternal(w, r, "browser")
}

// HandleWHIPDisconnect handles a publisher disconnect request
// from the OBS WHIP endpoint.
// DELETE /api/whip
func (s *Session) HandleWHIPDisconnect(w http.ResponseWriter, r *http.Request) {
	s.handleWHIPDelete(w)
}

// HandleWHIPDelete handles a publisher disconnect request
// from the browser WHIP endpoint.
// DELETE /api/whip/browser
func (s *Session) HandleWHIPDelete(w http.ResponseWriter, r *http.Request) {
	s.handleWHIPDelete(w)
}

// handleWHIPDelete cleans up the current publisher regardless of
// which WHIP endpoint was used. Idempotent — returns 200 even if
// no publisher is active (already cleaned up by ICE disconnect).
func (s *Session) handleWHIPDelete(w http.ResponseWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.publisherPC != nil {
		if err := s.publisherPC.Close(); err != nil {
			s.logger.Error().Err(err).
				Msg("whip: error closing publisher connection")
		}
		s.clearPublisher()
		s.logger.Info().Msg("whip: publisher disconnected via DELETE")
	}

	w.WriteHeader(http.StatusOK)
}
