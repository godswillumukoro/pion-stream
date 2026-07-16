package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/rs/zerolog"
)

// Session holds the global state for a single active stream.
type Session struct {
	config Config
	logger zerolog.Logger

	mu sync.Mutex

	publisherPC     *webrtc.PeerConnection
	publisherType   string
	startedAt       time.Time
	publisherTracks map[string]*publisherTrack
	viewers         []*webrtc.PeerConnection
	viewerTrackSets map[*webrtc.PeerConnection]*viewerTrackSet
	viewerSnapshot  atomic.Value

	// lastWHEPSDP stores the most recent WHEP SDP answer for debugging.
	lastWHEPSDP atomic.Value // string

	// chatHub manages the live chat message ring buffer and SSE fan-out.
	chatHub *ChatHub

	api    *webrtc.API
	udpMux *ice.MultiUDPMuxDefault
}

type publisherTrack struct {
	track *webrtc.TrackRemote
	codec webrtc.RTPCodecCapability
	kind  webrtc.RTPCodecType
}

type viewerTrackSet struct {
	viewerPC   *webrtc.PeerConnection
	audioTrack *webrtc.TrackLocalStaticRTP
	videoTrack *webrtc.TrackLocalStaticRTP
	isClosed   atomic.Bool

	// Packet counters for diagnostics.
	packetsWritten atomic.Int64

	videoHasKeyframe bool
	videoIsH264      bool
	videoLastPLI     time.Time
	videoMu          sync.Mutex

	audioMu sync.Mutex
}

func NewSession(cfg Config, logger zerolog.Logger) *Session {
	s := &Session{
		config:          cfg,
		logger:          logger,
		publisherTracks: make(map[string]*publisherTrack),
		viewerTrackSets: make(map[*webrtc.PeerConnection]*viewerTrackSet),
	}
	s.viewerSnapshot.Store([]*viewerTrackSet{})
	s.lastWHEPSDP.Store("")
	s.chatHub = newChatHub()

	settingEngine := webrtc.SettingEngine{}

	udpMux, err := ice.NewMultiUDPMuxFromPort(cfg.UDPMuxPort)
	if err != nil {
		logger.Fatal().Err(err).Int("port", cfg.UDPMuxPort).
			Msg("failed to create UDP mux")
	}
	s.udpMux = udpMux
	settingEngine.SetICEUDPMux(udpMux)

	if cfg.PublicIP != "" {
		settingEngine.SetNAT1To1IPs([]string{cfg.PublicIP}, webrtc.ICECandidateTypeHost)
	}
	settingEngine.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	settingEngine.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6})

	s.api = webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))
	return s
}

func (s *Session) registerViewerTracks(vts *viewerTrackSet) {
	s.mu.Lock()
	s.viewerTrackSets[vts.viewerPC] = vts
	s.buildViewerSnapshot()
	s.mu.Unlock()
}

func (s *Session) unregisterViewerTracks(pc *webrtc.PeerConnection) {
	s.mu.Lock()
	delete(s.viewerTrackSets, pc)
	s.buildViewerSnapshot()
	s.mu.Unlock()
}

func (s *Session) buildViewerSnapshot() {
	snap := make([]*viewerTrackSet, 0, len(s.viewerTrackSets))
	for _, vts := range s.viewerTrackSets {
		if !vts.isClosed.Load() {
			snap = append(snap, vts)
		}
	}
	s.viewerSnapshot.Store(snap)
}

func (s *Session) getViewerSnapshot() []*viewerTrackSet {
	v := s.viewerSnapshot.Load()
	if v == nil {
		return nil
	}
	return v.([]*viewerTrackSet)
}

func (s *Session) startBroadcastWriter(pt *publisherTrack) {
	isVideo := pt.kind == webrtc.RTPCodecTypeVideo
	isH264 := isVideo && pt.codec.MimeType == webrtc.MimeTypeH264
	depacketizer := getDepacketizer(pt.codec.MimeType)

	s.logger.Info().
		Str("track_id", pt.track.ID()).
		Str("kind", pt.track.Kind().String()).
		Str("codec", pt.codec.MimeType).
		Bool("isH264", isH264).
		Msg("broadcast writer started")

	go func() {
		var pktCount int64
		defer func() {
			s.logger.Info().
				Str("track_id", pt.track.ID()).
				Int64("packets_read", pktCount).
				Msg("broadcast writer stopped")
		}()

		rtpPkt := &rtp.Packet{}
		buf := make([]byte, 1500)

		for {
			n, _, readErr := pt.track.Read(buf)
			if readErr != nil {
				return
			}
			pktCount++

			if err := rtpPkt.Unmarshal(buf[:n]); err != nil {
				continue
			}

			isKeyframe := isPacketKeyframe(rtpPkt, isH264, depacketizer)
			rtpPkt.Extension = false
			rtpPkt.Extensions = nil

			snap := s.getViewerSnapshot()
			if len(snap) == 0 {
				// Log every 300th packet (roughly every 10s at 30fps) when no viewers.
				if pktCount%300 == 1 {
					s.logger.Debug().
						Str("track_id", pt.track.ID()).
						Int64("packet", pktCount).
						Msg("broadcast: no viewers, skipping")
				}
				continue
			}

			// Log first few packets with viewer count.
			if pktCount <= 3 || pktCount%300 == 0 {
				s.logger.Debug().
					Str("track_id", pt.track.ID()).
					Int64("packet", pktCount).
					Int("viewers", len(snap)).
					Bool("isKeyframe", isKeyframe).
					Msg("broadcast: writing to viewers")
			}

			for _, vts := range snap {
				if vts.isClosed.Load() {
					continue
				}
				if isVideo {
					s.writeVideoToViewer(vts, rtpPkt, isH264, isKeyframe)
				} else {
					s.writeAudioToViewer(vts, rtpPkt)
				}
			}
		}
	}()
}

func (s *Session) writeVideoToViewer(vts *viewerTrackSet, src *rtp.Packet, isH264, isKeyframe bool) {
	vts.videoMu.Lock()
	defer vts.videoMu.Unlock()

	if vts.videoTrack == nil {
		return
	}
	vts.videoIsH264 = isH264

	if isH264 && !vts.videoHasKeyframe {
		if isKeyframe {
			vts.videoHasKeyframe = true
		} else {
			if time.Since(vts.videoLastPLI) > time.Second {
				vts.videoLastPLI = time.Now()
				go s.sendPLI()
			}
			return
		}
	}

	if writeErr := vts.videoTrack.WriteRTP(src); writeErr != nil {
		vts.isClosed.Store(true)
	} else {
		vts.packetsWritten.Add(1)
	}
}

func (s *Session) writeAudioToViewer(vts *viewerTrackSet, src *rtp.Packet) {
	vts.audioMu.Lock()
	defer vts.audioMu.Unlock()

	if vts.audioTrack == nil {
		return
	}

	if writeErr := vts.audioTrack.WriteRTP(src); writeErr != nil {
		vts.isClosed.Store(true)
	} else {
		vts.packetsWritten.Add(1)
	}
}

// --- Status and UI ---

func (s *Session) setPublisher(pc *webrtc.PeerConnection, pubType string) {
	s.publisherPC = pc
	s.publisherType = pubType
	s.startedAt = time.Now()
}

func (s *Session) clearPublisher() {
	s.publisherPC = nil
	s.publisherType = ""
	s.startedAt = time.Time{}
	s.publisherTracks = make(map[string]*publisherTrack)
	// Start a fresh chat room for the next stream.
	s.chatHub.clear()
}

func (s *Session) isLive() bool { return s.publisherPC != nil }

func (s *Session) numViewers() int { return len(s.viewers) }

func (s *Session) HandleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	live := s.isLive()
	viewers := s.numViewers()
	pubType := s.publisherType
	durationSec := int64(0)
	if live {
		durationSec = int64(time.Since(s.startedAt).Seconds())
	}
	// Collect packet stats from all viewers.
	var totalPackets int64
	for _, vts := range s.viewerTrackSets {
		totalPackets += vts.packetsWritten.Load()
	}
	s.mu.Unlock()

	if r.Header.Get("HX-Request") == "true" {
		s.writeStatusHTML(w, live, viewers, pubType)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Live            bool   `json:"live"`
		PublisherType   string `json:"publisher_type"`
		Viewers         int    `json:"viewers"`
		DurationSeconds int64  `json:"duration_seconds"`
		PacketsWritten  int64  `json:"packets_written"`
	}{Live: live, PublisherType: pubType, Viewers: viewers,
		DurationSeconds: durationSec, PacketsWritten: totalPackets})
}

func (s *Session) writeStatusHTML(w http.ResponseWriter, live bool, viewers int, pubType string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if live {
		fmt.Fprintf(w,
			`<span id="status-badge" class="badge live" hx-get="/status" hx-trigger="every 5s" hx-swap="outerHTML"><span class="badge-dot"></span><span class="badge-label live">LIVE</span></span><span id="viewer-count" class="viewers" hx-swap-oob="true">%s</span><span id="footer-status" class="footer-item" hx-swap-oob="true"><span class="footer-label">UPLINK</span><span style="color:var(--accent)">CONNECTED</span></span>`,
			fmtViewerCount(viewers))
	} else {
		fmt.Fprintf(w,
			`<span id="status-badge" class="badge" hx-get="/status" hx-trigger="every 5s" hx-swap="outerHTML"><span class="badge-dot"></span><span class="badge-label offline">OFFLINE</span></span><span id="viewer-count" class="viewers" hx-swap-oob="true">&mdash;</span><span id="footer-status" class="footer-item" hx-swap-oob="true"><span class="footer-label">UPLINK</span><span>NO CARRIER</span></span>`)
	}
}

func fmtViewerCount(n int) string {
	if n == 1 {
		return "1 viewer"
	}
	return fmt.Sprintf("%d viewers", n)
}

func (s *Session) HandleSite(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(siteTemplate)
}

func (s *Session) HandleStudio(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(studioTemplate)
}

// HandleTestWHEP serves a minimal test page that directly connects via WHEP.
func (s *Session) HandleTestWHEP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(testWHEPTemplate)
}

func (s *Session) HandleViewer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(viewerTemplate)
}

// HandleDebugSDP returns the last WHEP SDP answer for debugging.
func (s *Session) HandleDebugSDP(w http.ResponseWriter, r *http.Request) {
	sdp := s.lastWHEPSDP.Load().(string)
	if sdp == "" {
		http.Error(w, "no WHEP answer yet", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(sdp))
}

// HandleDebugStatus returns detailed session state as JSON.
func (s *Session) HandleDebugStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	type viewerInfo struct {
		ID             string
		PacketsWritten int64
		HasVideoTrack  bool
		HasAudioTrack  bool
		Closed         bool
	}
	var viewers []viewerInfo
	for pc, vts := range s.viewerTrackSets {
		viewers = append(viewers, viewerInfo{
			ID:             fmt.Sprintf("%p", pc),
			PacketsWritten: vts.packetsWritten.Load(),
			HasVideoTrack:  vts.videoTrack != nil,
			HasAudioTrack:  vts.audioTrack != nil,
			Closed:         vts.isClosed.Load(),
		})
	}

	_ = json.NewEncoder(w).Encode(struct {
		Live          bool         `json:"live"`
		PublisherType string       `json:"publisher_type"`
		TrackCount    int          `json:"track_count"`
		ViewerCount   int          `json:"viewer_count"`
		Viewers       []viewerInfo `json:"viewers"`
	}{
		Live:          s.isLive(),
		PublisherType: s.publisherType,
		TrackCount:    len(s.publisherTracks),
		ViewerCount:   len(s.viewerTrackSets),
		Viewers:       viewers,
	})
}

func (s *Session) addViewer(pc *webrtc.PeerConnection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.viewers = append(s.viewers, pc)
}

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

var viewerIDCounter int

func generateViewerID() string {
	viewerIDCounter++
	return fmt.Sprintf("viewer-%d", viewerIDCounter)
}

func (s *Session) newAPI() *webrtc.API { return s.api }

func (s *Session) iceServers() []webrtc.ICEServer {
	servers := []webrtc.ICEServer{{URLs: []string{s.config.STUNServer}}}
	if s.config.TURNServer != "" {
		servers = append(servers, webrtc.ICEServer{
			URLs: []string{s.config.TURNServer}, Username: s.config.TURNUser, Credential: s.config.TURNPass,
		})
	}
	return servers
}

func appendCandidateToAnswer(sdp string) string {
	return sdp + "a=end-of-candidates\r\n"
}

func (s *Session) sendPLI() {
	s.mu.Lock()
	publisherPC := s.publisherPC
	packets := make([]rtcp.Packet, 0, len(s.publisherTracks))
	for _, pt := range s.publisherTracks {
		if pt.kind != webrtc.RTPCodecTypeVideo {
			continue
		}
		if mediaSSRC := uint32(pt.track.SSRC()); mediaSSRC != 0 {
			packets = append(packets, &rtcp.PictureLossIndication{
				MediaSSRC: mediaSSRC,
			})
		}
	}
	s.mu.Unlock()

	if publisherPC == nil || len(packets) == 0 {
		return
	}

	if err := publisherPC.WriteRTCP(packets); err != nil {
		s.logger.Error().Err(err).Msg("whip: failed to send PLI")
	}
}

// --- Keyframe detection ---

func getDepacketizer(mimeType string) rtp.Depacketizer {
	switch mimeType {
	case webrtc.MimeTypeH264:
		return &h264Depacketizer{}
	case webrtc.MimeTypeVP8:
		return &vp8Depacketizer{}
	default:
		return &passthroughDepacketizer{}
	}
}

func isPacketKeyframe(pkt *rtp.Packet, isH264 bool, depacketizer rtp.Depacketizer) bool {
	if !isH264 {
		return true
	}
	nalu, err := depacketizer.Unmarshal(pkt.Payload)
	if err != nil || len(nalu) < 1 {
		return false
	}
	firstNaluType := nalu[0] & 0x1F
	const idrNALU, spsNALU, ppsNALU = 5, 7, 8
	return firstNaluType == idrNALU || firstNaluType == spsNALU || firstNaluType == ppsNALU
}

type h264Depacketizer struct{}

func (d *h264Depacketizer) Unmarshal(payload []byte) ([]byte, error) {
	if len(payload) < 2 {
		return nil, fmt.Errorf("payload too short")
	}
	naluType := payload[0] & 0x1F
	switch {
	case naluType >= 1 && naluType <= 23:
		return payload, nil
	case naluType == 24:
		if len(payload) > 1 {
			return payload[1:], nil
		}
		return payload, nil
	case naluType == 28:
		if len(payload) > 1 {
			return []byte{payload[1] & 0x1F}, nil
		}
		return []byte{0}, nil
	default:
		return payload, nil
	}
}
func (d *h264Depacketizer) IsPartitionHead(payload []byte) bool { return true }
func (d *h264Depacketizer) IsPartitionTail(marker bool, payload []byte) bool {
	return marker
}

type vp8Depacketizer struct{}

func (d *vp8Depacketizer) Unmarshal(payload []byte) ([]byte, error) { return payload, nil }
func (d *vp8Depacketizer) IsPartitionHead(payload []byte) bool      { return true }
func (d *vp8Depacketizer) IsPartitionTail(marker bool, payload []byte) bool {
	return marker
}

type passthroughDepacketizer struct{}

func (d *passthroughDepacketizer) Unmarshal(payload []byte) ([]byte, error) { return payload, nil }
func (d *passthroughDepacketizer) IsPartitionHead(payload []byte) bool      { return true }
func (d *passthroughDepacketizer) IsPartitionTail(marker bool, payload []byte) bool {
	return marker
}

// Save SDP to /tmp for debugging.
func saveDebugSDP(name, sdp string) {
	_ = os.WriteFile("/tmp/"+name, []byte(sdp), 0644)
}
