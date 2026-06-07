// Command pion-stream — whip.go handles the WHIP (WebRTC-HTTP Ingest
// Protocol) endpoint. OBS Studio and other WHIP-compatible clients
// publish their stream by POSTing an SDP offer. The server creates a
// PeerConnection, sets up ICE/STUN, stores the incoming media tracks,
// and returns an SDP answer.
package main

import (
	"io"
	"net/http"
	"strings"

	"github.com/pion/webrtc/v4"
)

// HandleWHIP processes a WHIP ingest request. The publisher (OBS)
// sends an SDP offer in the request body with Content-Type
// application/sdp. The server establishes a WebRTC PeerConnection,
// stores the publisher's tracks, and returns an SDP answer.
//
// If a STREAM_KEY is configured, the request must include an
// Authorization: Bearer <key> header.
func (s *Session) HandleWHIP(w http.ResponseWriter, r *http.Request) {
	// Authenticate if a stream key is configured.
	if s.config.StreamKey != "" {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") ||
			strings.TrimPrefix(auth, "Bearer ") != s.config.StreamKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			s.logger.Warn().Msg("whip: rejected — invalid stream key")
			return
		}
	}

	// Read the SDP offer body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		s.logger.Error().Err(err).Msg("whip: failed to read request body")
		return
	}
	defer r.Body.Close()

	offerStr := string(body)
	if offerStr == "" {
		http.Error(w, "empty SDP offer", http.StatusBadRequest)
		s.logger.Warn().Msg("whip: empty SDP offer received")
		return
	}

	s.logger.Info().
		Int("offer_bytes", len(offerStr)).
		Msg("whip: received publish offer")

	// Prevent duplicate publishers — disconnect any existing one.
	s.mu.Lock()
	if s.publisherPC != nil {
		s.logger.Warn().Msg("whip: disconnecting existing publisher")
		_ = s.publisherPC.Close()
		s.publisherPC = nil
		s.publisherTracks = make(map[string]*webrtc.TrackRemote)
	}
	s.mu.Unlock()

	// Create the WebRTC API with the shared ICE UDP mux.
	api := s.newAPI()

	peerConnection, err := api.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{s.config.STUNServer}},
		},
	})
	if err != nil {
		http.Error(w, "failed to create peer connection", http.StatusInternalServerError)
		s.logger.Error().Err(err).Msg("whip: failed to create peer connection")
		return
	}

	// When the publisher sends media tracks, store them so they
	// can be relayed to WHEP viewers.
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		s.logger.Info().
			Str("track_id", track.ID()).
			Str("kind", track.Kind().String()).
			Msg("whip: received track from publisher")

		s.mu.Lock()
		s.publisherTracks[track.ID()] = track
		s.mu.Unlock()
	})

	// Handle ICE connection state changes for logging.
	peerConnection.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		s.logger.Info().
			Str("state", state.String()).
			Msg("whip: ICE connection state changed")

		if state == webrtc.ICEConnectionStateDisconnected ||
			state == webrtc.ICEConnectionStateFailed ||
			state == webrtc.ICEConnectionStateClosed {
			s.mu.Lock()
			if s.publisherPC == peerConnection {
				s.publisherPC = nil
				s.publisherTracks = make(map[string]*webrtc.TrackRemote)
				s.logger.Info().Msg("whip: publisher disconnected")
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
		http.Error(w, "failed to set remote description", http.StatusBadRequest)
		s.logger.Error().Err(err).Msg("whip: failed to set remote description")
		_ = peerConnection.Close()
		return
	}

	// Create an SDP answer.
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		http.Error(w, "failed to create answer", http.StatusInternalServerError)
		s.logger.Error().Err(err).Msg("whip: failed to create answer")
		_ = peerConnection.Close()
		return
	}

	// Set the local description (our answer).
	if err := peerConnection.SetLocalDescription(answer); err != nil {
		http.Error(w, "failed to set local description", http.StatusInternalServerError)
		s.logger.Error().Err(err).Msg("whip: failed to set local description")
		_ = peerConnection.Close()
		return
	}

	// Store the publisher's peer connection.
	s.mu.Lock()
	s.publisherPC = peerConnection
	s.mu.Unlock()

	// Return the SDP answer with WHIP-specific headers.
	// The Location header tells WHIP clients where to send DELETE.
	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("Location", "/api/whip")
	w.Header().Set("Access-Control-Expose-Headers", "Location, ETag")
	w.WriteHeader(http.StatusCreated)
	if _, err := w.Write([]byte(answer.SDP)); err != nil {
		s.logger.Error().Err(err).Msg("whip: failed to write answer SDP")
	}

	s.logger.Info().
		Int("answer_bytes", len(answer.SDP)).
		Msg("whip: publisher connected successfully")
}

// HandleWHIPDisconnect handles a publisher disconnect request.
// The WHIP client sends DELETE to the resource URL to end the session.
func (s *Session) HandleWHIPDisconnect(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.publisherPC == nil {
		http.Error(w, "no active publisher", http.StatusNotFound)
		return
	}

	if err := s.publisherPC.Close(); err != nil {
		s.logger.Error().Err(err).Msg("whip: error closing publisher connection")
	}

	s.publisherPC = nil
	s.publisherTracks = make(map[string]*webrtc.TrackRemote)

	w.WriteHeader(http.StatusOK)
	s.logger.Info().Msg("whip: publisher disconnected via DELETE")
}
