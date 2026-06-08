# pion-stream

A self-hosted live streaming server built on Pion WebRTC.
Publish from OBS via WHIP. Watch in any browser via WHEP.
Single Go binary. No transcoding. Ultra-low latency.

## Live Demo

```
Viewer:  https://stream.talcr.com
Landing: https://stream.talcr.com/site
Studio:  https://stream.talcr.com/studio
Health:  https://stream.talcr.com/health
Status:  https://stream.talcr.com/status
```

## Quick Start

```bash
git clone https://github.com/godswillumukoro/pion-stream
cd pion-stream
./scripts/setup.sh
```

Then open `https://YOUR_SERVER_IP` in your browser.

## How to Test Live Streaming

### 1. Check the server is running

```bash
curl https://stream.talcr.com/health
# → OK
```

### 2. Publish from OBS Studio

1. Open OBS Studio
2. **Settings → Stream → Service:** Custom
3. **Server:** `https://stream.talcr.com/api/whip`
4. **Stream Key:** leave blank
5. **Settings → Output → Encoder:** x264 (H.264)
6. Click **Start Streaming**

### 3. Watch in any browser

Open `https://stream.talcr.com` — the stream appears automatically.

### 4. Verify the status

```bash
curl https://stream.talcr.com/status
# → {"live":true,"viewers":1}
```

### 5. Test with multiple viewers

Open the viewer URL in multiple browser tabs. The viewer count
increments with each connection.

### 6. Test WHIP auth (optional)

Set `STREAM_KEY=mysecret` in `.env`, restart the server, then
pass the key in OBS as the stream key or via header:

```bash
curl -X POST https://stream.talcr.com/api/whip \
  -H "Authorization: Bearer mysecret" \
  -H "Content-Type: application/sdp" \
  --data-binary @offer.sdp
```

## Publish from OBS (Quick Reference)

1. Open OBS Studio
2. Settings → Stream → Service: **Custom**
3. Server: `https://YOUR_SERVER_IP/api/whip`
4. Stream Key: leave blank (or set `STREAM_KEY` in `.env`)
5. Output → Encoder: **H.264** (x264)
6. Click **Start Streaming**

See `scripts/configure-obs.md` for detailed instructions with screenshots.

## Server Endpoints

| Method | Path | Description | Response |
|--------|------|-------------|----------|
| `GET` | `/` | Stream viewer UI (HTMX) | HTML |
| `GET` | `/site` | Companion landing page | HTML |
| `GET` | `/studio` | Browser-based broadcast studio | HTML |
| `GET` | `/test` | Minimal WHEP connectivity test | HTML |
| `GET` | `/health` | Health check | `200 OK` |
| `GET` | `/status` | Stream status | JSON (live, viewers, duration, packets) |
| `GET` | `/debug/sdp` | Last WHEP SDP answer | plain text |
| `GET` | `/debug/status` | Detailed session state | JSON |
| `POST` | `/api/whip` | WHIP ingest (OBS → server) | SDP answer |
| `DELETE` | `/api/whip` | Publisher disconnect | `200 OK` |
| `POST` | `/api/whip/browser` | WHIP ingest (browser → server) | SDP answer |
| `DELETE` | `/api/whip/browser` | Browser publisher disconnect | `200 OK` |
| `POST` | `/api/whep` | WHEP egress (server → browser) | SDP answer |

## Configuration

All settings via environment variables (see `.env.example`):

| Variable | Default | Description |
|---|---|---|
| `PORT` | `80` | HTTP server port |
| `UDP_MUX_PORT` | `3000` | ICE UDP mux port (single port) |
| `PUBLIC_IP` | (auto) | VPS public IP for ICE candidates |
| `STUN_SERVER` | `stun:stun.l.google.com:19302` | STUN server URL |
| `STREAM_KEY` | (none) | Bearer token for WHIP auth |

## Stack

- **Go + Chi** — HTTP routing and middleware
- **Pion WebRTC** — Pure-Go WebRTC implementation
- **HTMX** — Dynamic UI without JavaScript frameworks
- **Systemd** — Native Linux service management

## Commands

```bash
make build      # Compile binary for linux/amd64
make run        # Run locally with defaults
make deploy HOST=<ip>  # Deploy to remote server
make clean      # Remove artifacts
```

## Learn More

- Video walkthrough: [YouTube link]
- WebRTC for the Streamer: https://webrtcforthestreamer.com
- Pion WebRTC: https://github.com/pion/webrtc
- WHIP spec: https://www.ietf.org/archive/id/draft-ietf-wish-whip-01.txt

## License

MIT
