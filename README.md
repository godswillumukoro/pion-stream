# pion-stream

A self-hosted browser-to-browser live streaming server built on Pion WebRTC.
Publish from your browser at `/studio`. Watch at `/`. Single Go binary. No transcoding. Ultra-low latency.

## Live Demo

| Page | URL |
|------|-----|
| **Watch a stream** | `https://stream.talcr.com` |
| **Publish from browser** | `https://stream.talcr.com/studio` |
| **Landing / docs** | `https://stream.talcr.com/site` |
| **WHEP connectivity test** | `https://stream.talcr.com/test` |
| **Health check** | `https://stream.talcr.com/health` |
| **API status (JSON)** | `https://stream.talcr.com/status` |
| **Debug: SDP** | `https://stream.talcr.com/debug/sdp` |
| **Debug: full state** | `https://stream.talcr.com/debug/status` |

## Quick Start

```bash
git clone https://github.com/godswillumukoro/pion-stream
cd pion-stream
sudo ./scripts/setup.sh
```

Then open `https://YOUR_SERVER_IP` in your browser.

## How It Works

```
Browser (/studio) ──WHIP──→ pion-stream ──WHEP──→ Browser (/)
                              │
                              └── RTP packet fan-out (no transcoding)
```

1. **Publisher** opens `/studio`, selects camera + mic, clicks **Go Live**. The browser's `getUserMedia` stream is sent to the server via WHIP (WebRTC-HTTP Ingest Protocol).
2. **Server** receives RTP packets and fans them out to every connected viewer — no transcoding, no re-encoding.
3. **Viewers** open `/` — HTMX polls for stream status. When live, a WHEP (WebRTC-HTTP Egress) connection is established automatically. Video appears. No page reload.

### Publish from your browser — no OBS required

Open `/studio` in Chrome or Firefox:

- Select your camera and microphone
- Watch the live preview with real-time audio meter
- Click **Go Live**

The built-in studio is a full WHIP client in vanilla JavaScript — no SDK, no build step, no downloads. View-source to see how it works.

### Or publish from OBS Studio

The WHIP endpoint at `/api/whip` accepts standard WHIP offers from any encoder:

1. OBS → Settings → Stream → Service: **Custom**
2. Server: `https://YOUR_SERVER_IP/api/whip`
3. Output → Encoder: **x264 (H.264)**
4. Click **Start Streaming**

Both the browser studio and OBS use the same protocol — viewers see the stream regardless of which client published it.

## Server Endpoints

| Method | Path | Description | Response |
|--------|------|-------------|----------|
| `GET` | `/` | Stream viewer UI (HTMX + WHEP) | HTML |
| `GET` | `/studio` | Browser-based broadcast studio | HTML |
| `GET` | `/site` | Companion landing page | HTML |
| `GET` | `/test` | Minimal WHEP connectivity test page | HTML |
| `GET` | `/health` | Health check | `200 OK` |
| `GET` | `/status` | Stream status (live, viewers, duration, packets) | JSON or HTML |
| `GET` | `/debug/sdp` | Last WHEP SDP answer | plain text |
| `GET` | `/debug/status` | Detailed session state (per-viewer stats) | JSON |
| `POST` | `/api/whip` | WHIP ingest — OBS or any WHIP client | SDP answer |
| `DELETE` | `/api/whip` | Publisher disconnect | `200 OK` |
| `POST` | `/api/whip/browser` | WHIP ingest — browser publisher | SDP answer |
| `DELETE` | `/api/whip/browser` | Browser publisher disconnect | `200 OK` |
| `POST` | `/api/whep` | WHEP egress — viewer requests stream | SDP answer |

## Configuration

All settings via environment variables (see `.env.example`):

| Variable | Default | Description |
|---|---|---|
| `PORT` | `80` | HTTP server listen port |
| `UDP_MUX_PORT` | `3000` | Single UDP port for all ICE connections |
| `PUBLIC_IP` | (auto) | Server public IP for ICE host candidates |
| `STUN_SERVER` | `stun:stun.l.google.com:19302` | STUN server URL for NAT traversal |
| `TURN_SERVER` | (none) | TURN server URL for relay fallback |
| `TURN_USER` | (none) | TURN server username |
| `TURN_PASS` | (none) | TURN server password |
| `STREAM_KEY` | (none) | Optional bearer token for WHIP auth |

## Architecture

| Decision | Why |
|----------|-----|
| **Pure Go, no CGO** | Cross-compile from anywhere. True static binary. |
| **ICE mux, single UDP port** | All peer connections multiplexed through one socket. One firewall rule. |
| **Packet fan-out, no transcoding** | One read per RTP packet, N writes for N viewers. Microsecond server latency. |
| **Embedded HTML templates** | `//go:embed` compiles all 4 pages into the binary. Single deployable artifact. |
| **H.264 keyframe awareness** | New viewers wait for an IDR/SPS frame before receiving video. No green/garbled first frames. |
| **PLI on viewer connect** | Each new viewer triggers a Picture Loss Indication — forces an immediate keyframe. |
| **HTMX for status polling** | Viewer page uses HTMX to poll `/status` every 5s. WebRTC connection is vanilla JS. |
| **Caddy reverse proxy** | Auto-HTTPS via Let's Encrypt. Go binary doesn't need to know about TLS. |
| **Systemd service** | Native Linux process supervision. Auto-restart on crash. Logs to journald. |

## Stack

- **Go + Chi** — HTTP routing and middleware
- **Pion WebRTC v4** — Pure-Go WebRTC implementation (ICE, DTLS, SRTP, SCTP)
- **HTMX** — Dynamic UI without JavaScript frameworks
- **Caddy** — Reverse proxy with automatic HTTPS
- **Systemd** — Native Linux service management

## Commands

```bash
make build                  # Compile static binary for linux/amd64
make run                    # Run locally with defaults
make deploy HOST=<ip>       # Deploy to remote server via setup script
make clean                  # Remove build artifacts
```

### Deploy with rsync (alternative)

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o streaming-server ./server/
rsync -avz --exclude '.git' . user@yourserver:/opt/pion-stream/
ssh user@yourserver "systemctl restart pion-stream"
```

## Firewall Requirements

```bash
ufw allow 80/tcp      # HTTP / Caddy
ufw allow 443/tcp     # HTTPS / Caddy
ufw allow 3000/udp    # WebRTC ICE (single mux port)
ufw allow 3478/tcp    # TURN (optional)
ufw allow 3478/udp    # TURN UDP (optional)
```

## Learn More

- [Video walkthrough](https://youtube.com/@godswillumukoro) — build series on YouTube
- [WebRTC for the Streamer](https://webrtcforthestreamer.com) — free WebRTC course
- [Pion WebRTC](https://github.com/pion/webrtc) — the Go WebRTC library
- [WHIP spec](https://www.ietf.org/archive/id/draft-ietf-wish-whip-01.txt) — IETF draft
- [broadcast-box](https://github.com/Sean-Der/broadcast-box) — the original broadcast box reference implementation

## License

MIT
