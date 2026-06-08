// Command pion-stream — whep.go handles the WHEP (WebRTC-HTTP Egress
// Protocol) endpoint. Browser-based viewers POST their SDP offer to
// receive the stream. Tracks are registered with the session's
// broadcast system, which fans out RTP packets to all viewers.
package main

import (
	"io"
	"net/http"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// HandleWHEP processes a WHEP egress request.
func (s *Session) HandleWHEP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if !s.isLive() {
		s.mu.Unlock()
		http.Error(w, "no active stream", http.StatusServiceUnavailable)
		return
	}
	tracks := make(map[string]*publisherTrack, len(s.publisherTracks))
	for id, pt := range s.publisherTracks {
		tracks[id] = pt
	}
	s.mu.Unlock()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	offerStr := string(body)
	if offerStr == "" {
		http.Error(w, "empty SDP offer", http.StatusBadRequest)
		return
	}

	s.logger.Info().Int("offer_bytes", len(offerStr)).Int("track_count", len(tracks)).Msg("whep: received viewer offer")

	api := s.newAPI()
	viewerPC, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: s.iceServers()})
	if err != nil {
		http.Error(w, "failed to create peer connection", http.StatusInternalServerError)
		return
	}

	viewerID := generateViewerID()
	s.addViewer(viewerPC)
	vts := &viewerTrackSet{viewerPC: viewerPC}

	// Add tracks BEFORE SetRemoteDescription — matching broadcast-box's
	// approach where local tracks are added first, then the viewer's
	// offer is processed. This lets Pion properly negotiate codecs.
	addTrackErrors := 0
	for _, pt := range tracks {
		localTrack, err := webrtc.NewTrackLocalStaticRTP(pt.codec, pt.track.ID(), pt.track.StreamID())
		if err != nil {
			addTrackErrors++
			s.logger.Error().Err(err).Str("track_id", pt.track.ID()).Msg("whep: NewTrackLocalStaticRTP failed")
			continue
		}

		rtpSender, err := viewerPC.AddTrack(localTrack)
		if err != nil {
			addTrackErrors++
			s.logger.Error().Err(err).Str("track_id", pt.track.ID()).Msg("whep: AddTrack failed")
			continue
		}

		if pt.kind == webrtc.RTPCodecTypeVideo {
			vts.videoTrack = localTrack
			s.startViewerRTCPReader(viewerID, rtpSender)
		} else {
			vts.audioTrack = localTrack
		}
	}

	s.logger.Info().
		Str("viewer_id", viewerID).
		Bool("has_video", vts.videoTrack != nil).
		Bool("has_audio", vts.audioTrack != nil).
		Int("add_errors", addTrackErrors).
		Msg("whep: tracks added")

	// Now set remote description — the viewer's recvonly offer.
	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerStr}
	if err := viewerPC.SetRemoteDescription(offer); err != nil {
		http.Error(w, "failed to set remote description", http.StatusBadRequest)
		s.removeViewer(viewerPC)
		_ = viewerPC.Close()
		return
	}

	s.registerViewerTracks(vts)
	s.sendPLI()

	viewerPC.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		s.logger.Info().Str("viewer_id", viewerID).Str("state", state.String()).Msg("whep: ICE state")
		if state == webrtc.ICEConnectionStateConnected {
			s.sendPLI()
		}
		if state == webrtc.ICEConnectionStateDisconnected ||
			state == webrtc.ICEConnectionStateFailed ||
			state == webrtc.ICEConnectionStateClosed {
			vts.isClosed.Store(true)
			s.unregisterViewerTracks(viewerPC)
			s.removeViewer(viewerPC)
		}
	})

	answer, err := viewerPC.CreateAnswer(nil)
	if err != nil {
		http.Error(w, "failed to create answer", http.StatusInternalServerError)
		s.unregisterViewerTracks(viewerPC)
		s.removeViewer(viewerPC)
		_ = viewerPC.Close()
		return
	}
	if err := viewerPC.SetLocalDescription(answer); err != nil {
		http.Error(w, "failed to set local description", http.StatusInternalServerError)
		s.unregisterViewerTracks(viewerPC)
		s.removeViewer(viewerPC)
		_ = viewerPC.Close()
		return
	}

	<-webrtc.GatheringCompletePromise(viewerPC)
	answerSDP := appendCandidateToAnswer(viewerPC.LocalDescription().SDP)

	// Save for debugging.
	s.lastWHEPSDP.Store(answerSDP)
	saveDebugSDP("last_whep_answer.sdp", answerSDP)

	preview := answerSDP
	if len(preview) > 500 {
		preview = preview[:500]
	}
	s.logger.Info().Str("viewer_id", viewerID).Int("answer_bytes", len(answerSDP)).Str("sdp_preview", preview).Msg("whep: viewer connected successfully")

	w.Header().Set("Content-Type", "application/sdp")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(answerSDP))
}

func (s *Session) startViewerRTCPReader(viewerID string, rtpSender *webrtc.RTPSender) {
	go func() {
		for {
			packets, _, err := rtpSender.ReadRTCP()
			if err != nil {
				return
			}

			for _, packet := range packets {
				switch packet.(type) {
				case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
					s.logger.Debug().
						Str("viewer_id", viewerID).
						Msg("whep: forwarding keyframe request to publisher")
					s.sendPLI()
				}
			}
		}
	}()
}
