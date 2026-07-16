# pion-stream

A self-hosted live streaming server built with Go and Pion WebRTC,
inspired by [Broadcast Box](https://github.com/glimesh/broadcast-box).

Publish from your browser or OBS via WHIP.
Watch in any browser via WHEP.
Single Go binary. No transcoding. Sub-500ms latency.

## Live Demo

| Page | URL |
|------|-----|
| Viewer | https://stream.talcr.com |
| Studio | https://stream.talcr.com/studio |
| Landing | https://stream.talcr.com/site |
| Health | https://stream.talcr.com/health |
| Status | https://stream.talcr.com/status |

## Quick Start

```bash
git clone https://github.com/godswillumukoro/pion-stream
cd pion-stream
sudo ./scripts/setup.sh
```

Then open `http://YOUR_SERVER_IP` in your browser.

## How It Works

```
Browser (/studio) ──WHIP──→ pion-stream ──WHEP──→ Browser (/)
                              │
                              └── RTP packet fan-out (no transcoding)
```

1. **Publisher** opens `/studio`, selects camera and mic, clicks **Go Live**. The browser's `getUserMedia` stream is sent to the server via WHIP (WebRTC-HTTP Ingest Protocol).
2. **Server** receives RTP packets and fans them out to every connected viewer — no transcoding, no re-encoding.
3. **Viewers** open `/` — HTMX polls `/status` every 5 seconds for live/offline state. When live, a WHEP (WebRTC-HTTP Egress) connection is established automatically. Video appears without a page reload.

### Publish from your browser

Open `/studio` in Chrome or Firefox:

- Select your camera and microphone
- Watch the live preview with real-time audio meter
- Click **Go Live**

The built-in studio is a full WHIP client in vanilla JavaScript — no SDK, no build step.

### Publish from OBS Studio

The WHIP endpoint at `/api/whip` accepts standard WHIP offers from any encoder:

1. OBS → Settings → Stream → Service: **Custom**
2. Server: `https://YOUR_SERVER_IP/api/whip`
3. Output → Encoder: **x264 (H.264)**
4. Click **Start Streaming**

Both the browser studio and OBS use the same protocol — viewers see the stream regardless of which client published it.

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/` | Viewer UI (HTMX + WHEP) |
| `GET` | `/studio` | Browser broadcast studio |
| `GET` | `/site` | Companion landing page |
| `GET` | `/test` | WHEP connectivity test page |
| `GET` | `/health` | Health check (`200 OK`) |
| `GET` | `/status` | Stream status — HTML (HTMX) or JSON |
| `GET` | `/debug/sdp` | Last WHEP SDP answer (plain text) |
| `GET` | `/debug/status` | Detailed session state (JSON) |
| `POST` | `/api/whip` | WHIP ingest (OBS or any WHIP client) |
| `DELETE` | `/api/whip` | Publisher disconnect |
| `POST` | `/api/whip/browser` | WHIP ingest (browser publisher) |
| `DELETE` | `/api/whip/browser` | Browser publisher disconnect |
| `POST` | `/api/whep` | WHEP egress (viewer requests stream) |
| `POST` | `/api/chat` | Send a chat message |
| `GET` | `/api/chat/events` | Live chat SSE stream |

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
| Pure Go, no CGO | Cross-compile from anywhere. Fully static binary. |
| ICE mux, single UDP port | All peer connections multiplexed through one socket. One firewall rule. |
| Packet fan-out, no transcoding | One read per RTP packet, N writes for N viewers. Microsecond server latency. |
| Embedded HTML templates | `//go:embed` compiles all pages into the binary. Single deployable artifact. |
| H.264 keyframe awareness | New viewers wait for an IDR/SPS frame before receiving video. No garbled first frames. |
| PLI on viewer connect | Each new viewer triggers a Picture Loss Indication to force an immediate keyframe. |
| HTMX for status polling | Viewer page polls `/status` every 5s. WebRTC connection is vanilla JavaScript. |
| SSE for live chat | Server-Sent Events fan out chat messages to all viewers in real time. |
| Caddy reverse proxy | Auto-HTTPS via Let's Encrypt. Go binary doesn't need to handle TLS. |
| Systemd service | Native Linux process supervision. Auto-restart on crash. Logs to journald. |

## Live Chat

The server includes a built-in live chat system:

- `POST /api/chat` — send a message (`{"name": "...", "text": "..."}`)
- `GET /api/chat/events` — SSE stream of all new messages
- Ring buffer holds the last 100 messages; new joiners receive recent history
- Chat room is cleared automatically when a stream ends

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
make deploy HOST=<ip>       # Deploy to remote server via SSH
make clean                  # Remove build artifacts
```

## Firewall Requirements

```bash
ufw allow 80/tcp      # HTTP / Caddy
ufw allow 443/tcp     # HTTPS / Caddy
ufw allow 3000/udp    # WebRTC ICE (single mux port)
```

## Built on

- [Pion WebRTC](https://github.com/pion/webrtc) — the Go WebRTC library
- [Broadcast Box](https://github.com/glimesh/broadcast-box) — the original reference implementation
- [WebRTC for the Streamer](https://webrtcforthestreamer.com) — free WebRTC course

## License

MIT
