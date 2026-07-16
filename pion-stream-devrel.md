# pion-stream — What I Built & Why Builders Should Care

> A DevRel-ready narrative by **Godswill Umukoro** — for engineers who ship.

---

## The product, in one sentence

**pion-stream** is a browser-to-browser live streaming server. Open `/studio` in one tab to broadcast your camera. Open `/` in another to watch. No OBS. No downloads. No accounts. Just Go and WebRTC.

---

## What it actually does — the real user flow

### To publish (10 seconds, zero setup):

1. Open `https://stream.talcr.com/studio`
2. Your browser asks for camera + microphone permission
3. The page populates device selects, shows a live preview with an audio meter
4. Click **Go Live**

That's it. Behind one button, 40 lines of vanilla JavaScript execute the entire WHIP protocol: create a peer connection, add local tracks, generate an SDP offer, POST it to `/api/whip/browser`, and set the remote answer. No SDK. No build step. View-source is the documentation.

### To watch (instant):

1. Open `https://stream.talcr.com`
2. HTMX polls `/status` every 5 seconds
3. When the stream goes live, the page dynamically establishes a WHEP connection
4. Video appears. Viewer count increments. No page reload.

### The architecture:

```
Browser (/studio) ──WHIP──→ pion-stream server ──WHEP──→ Browser (/)
                             │
                             └── RTP packet fan-out (no transcoding)
                             └── Single UDP port for all ICE (mux)
```

If someone wants to use OBS Studio instead of the browser studio, `/api/whip` accepts standard WHIP offers — same endpoint, same protocol, any WHIP client. But the primary experience is browser-native.

---

## Why builders should find this compelling

### 1. Browser-to-browser in under 100 lines of JavaScript

The studio's entire WHIP client is 40 lines. The viewer's entire WHEP client + HTMX integration is ~80 lines. Both are vanilla JavaScript — no React, no npm, no webpack, no TypeScript compiler. A developer can open DevTools, read the source, and understand the full WebRTC signaling flow in 5 minutes.

This is the "broadcast box" pattern — popularized by Sean DuBois's broadcast-box project — but reimagined as a Go server with an opinionated browser-first UX. The code is structured to be *taught*, not just executed.

### 2. The browser studio is the demo

For DevRel, the hardest problem is "how do I show this in 30 seconds?" pion-stream solves it with the `/studio` endpoint. No pre-recorded video. No "install OBS first." No "configure these settings." Just open a URL and click a button.

The studio is also genuinely good — it has:
- Camera and microphone device enumeration with labels
- Real-time audio level meter (Web Audio API AnalyserNode, 60fps)
- Stream duration timer synced with the server
- Live viewer count
- Connection quality indicator (Excellent / Good / Degraded based on packet loss and RTT from `RTCPeerConnection.getStats()`)
- Graceful error handling for every `getUserMedia` failure mode (NotAllowedError, NotFoundError, NotReadableError, OverconstrainedError)

This isn't a toy. It's a production-quality WebRTC publisher UI.

### 3. Single static binary. Single UDP port.

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o streaming-server ./server/
# → 11 MB static binary.
```

No Docker. No Python. No Node. No FFmpeg. No C libraries. The binary embeds all four HTML templates at compile time via Go's `//go:embed` directive. Drop it on a $5 Linode and it runs.

WebRTC normally requires a port range for ICE candidates. pion-stream uses Pion's `ICEUDPMux` to multiplex every peer connection — publisher *and* all viewers — through a single UDP port (default 3000). One firewall rule:

```bash
ufw allow 3000/udp
```

### 4. Standards-based, not proprietary

The server speaks **WHIP** (WebRTC-HTTP Ingest Protocol) and **WHEP** (WebRTC-HTTP Egress Protocol) — emerging IETF standards. The endpoints are:

| Method | Path | What it does |
|--------|------|-------------|
| `POST` | `/api/whip` | WHIP ingest — OBS or any WHIP client publishes here |
| `POST` | `/api/whip/browser` | Same protocol, separate route for the browser studio |
| `DELETE` | `/api/whip` | Teardown — publisher disconnects |
| `POST` | `/api/whep` | WHEP egress — viewer requests the stream |

Any WHIP-compatible encoder works. Any WHEP-compatible player works. The bundled UI is just one option — the protocol is the contract.

### 5. Packet fan-out, not transcoding

The broadcast writer reads each RTP packet from the publisher *once*, then writes it to every viewer's local track:

```go
n, _, _ := pt.track.Read(buf)
rtpPkt.Unmarshal(buf[:n])

for _, vts := range snap {
    vts.videoTrack.WriteRTP(rtpPkt) // one read, N writes
}
```

Server-added latency: measured in microseconds. No re-encoding. No FFmpeg. Frame-for-frame what the publisher sends is what viewers see. This is how real broadcast infrastructure works at the packet level.

### 6. Thoughtful WebRTC details built in

| Feature | Where | Why |
|---------|-------|-----|
| **Keyframe gate** | `session.go:211` | New H.264 viewers wait for an IDR frame before receiving video — prevents green/garbled first frames |
| **PLI on connect** | `whep.go:123` | Sends a Picture Loss Indication to the publisher when a viewer joins, requesting an immediate keyframe |
| **RTCP relay** | `whep.go:178` | Forwards PLI/FIR from viewers to publisher so the publisher knows to send keyframes |
| **Atomic viewer snapshots** | `session.go:106` | `atomic.Value` stores a read-only copy of viewer tracks — broadcast writer reads without holding a lock |
| **WriteRTP failure detection** | `session.go:222` | If `WriteRTP` returns an error (viewer's transport died), the track set is marked closed |
| **ICE mux** | `session.go:76` | One UDP socket, multiplexed — firewall-friendly, single port |
| **NAT1To1IP** | `session.go:81` | Server behind NAT? Set `PUBLIC_IP` and ICE candidates advertise the correct address |
| **Multicast DNS disabled** | `session.go:83` | `.local` ICE candidates are noise on a VPS — disabled intentionally |

### 7. Deployed, live, observable

The server is running at `https://stream.talcr.com` behind Caddy (auto-HTTPS via Let's Encrypt) with systemd supervision. Debug endpoints are public:

```bash
curl https://stream.talcr.com/health          # → OK
curl https://stream.talcr.com/status          # → {"live":true,"viewers":3,...}
curl https://stream.talcr.com/debug/status    # → full session state with per-viewer stats
curl https://stream.talcr.com/debug/sdp       # → last WHEP SDP answer
```

This is not a README with "coming soon" badges. It's running, testable, and observable right now.

---

## Architecture decisions worth highlighting

| Decision | Why |
|----------|-----|
| **Pure Go, no CGO** | Cross-compile from macOS to Linux. True static binary. No libc dependency. |
| **Embedded HTML templates** | `//go:embed` compiles all four HTML pages into the binary. Single artifact to deploy. No `templates/` directory on the server. |
| **ICE mux, single port** | `ice.MultiUDPMuxDefault` — one UDP socket per interface, all peer connections multiplexed. Operational simplicity over theoretical purity. |
| **Atomic viewer snapshots** | Lock-free reads in the hot path (broadcast writer). The `sync.Mutex` is only held during viewer connect/disconnect. |
| **Chi router** | Lightweight, idiomatic, composable middleware. Not a framework — a `net/http` compatible router. |
| **HTMX for status only** | The viewer page uses HTMX *only* for polling `/status`. The WebRTC connection itself is vanilla JavaScript. No framework lock-in for the real-time part. |
| **Zerolog** | Structured JSON logging to stdout. systemd/journald picks it up. Easy to grep, parse, ship to Loki/CloudWatch. |
| **Caddy reverse proxy** | Separation of concerns. The Go binary doesn't know about TLS, certificates, or domain names. Caddy handles HTTPS and proxies to `localhost:8080`. |
| **Graceful shutdown** | `signal.Notify` + `server.Shutdown()` with a 10-second timeout. All peer connections are allowed to close cleanly. |

---

## The numbers that matter

| Metric | Value |
|--------|-------|
| Binary size | 11 MB (stripped, static, includes 4 HTML templates) |
| Memory at idle | 1.3 MB |
| Memory per viewer | negligible (shared RTP packet references) |
| Server-added latency | < 1 ms (packet write overhead) |
| Lines of Go | ~800 (excluding templates) |
| Lines of JS (studio) | ~240 (including UI orchestration) |
| Lines of JS (viewer) | ~80 (including HTMX integration) |
| Go dependencies | 2 top-level (chi, zerolog) + Pion stack |
| Time to demo | ~10 seconds (open `/studio`, click Go Live) |

---

## What this says about the builder

For a DevRel role, this project demonstrates:

- **Technical depth**: WHIP/WHEP protocol implementation, ICE mux, H.264 NAL unit parsing, keyframe detection, PLI/NACK handling, RTCP relay — these aren't surface-level WebRTC concepts.
- **Developer empathy**: The setup script is idempotent. The code comments explain *why*, not *what*. The browser studio handles every `getUserMedia` error with a user-friendly message. The debug endpoints make troubleshooting self-serve.
- **Product intuition**: The browser studio wasn't necessary for the core server. But it makes the product *demoable in 10 seconds*. That's the difference between a library and an experience. That's DevRel instinct.
- **Communication**: The code is structured to be read in order — `main.go` → `config.go` → `session.go` → `whip.go` → `whep.go`. Every file has a package comment. Every function has a doc comment. This is a codebase built to be *shown* to other developers.
- **Shipping mentality**: The server is live. Health checks pass. Debug endpoints work. This isn't a portfolio piece with "TODO: deploy" — it's running on a Linode right now, serving real WebRTC connections.

---

## Quick links

| Resource | URL |
|----------|-----|
| **Watch a stream** | `https://stream.talcr.com` |
| **Publish from browser** | `https://stream.talcr.com/studio` |
| **Landing page** | `https://stream.talcr.com/site` |
| **WHEP connectivity test** | `https://stream.talcr.com/test` |
| **Health check** | `https://stream.talcr.com/health` |
| **API status (JSON)** | `https://stream.talcr.com/status` |
| **Debug: SDP** | `https://stream.talcr.com/debug/sdp` |
| **Debug: full state** | `https://stream.talcr.com/debug/status` |
| **Source code** | `https://github.com/godswillumukoro/pion-stream` |

---

*Built with Go, Pion WebRTC, HTMX, and the philosophy that developer tools should be demoable in 10 seconds.*
