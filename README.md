# pion-stream

A self-hosted live streaming server built on Pion WebRTC.
Publish from OBS via WHIP. Watch in any browser via WHEP.
Single Go binary. No transcoding. Sub-500ms latency.

## Quick Start

```bash
git clone https://github.com/godswill/pion-stream
cd pion-stream
./scripts/setup.sh
```

Then open `http://YOUR_SERVER_IP:8080` in your browser.

## Publish from OBS

1. Open OBS Studio
2. Settings → Stream → Service: **Custom**
3. Server: `http://YOUR_SERVER_IP:8080/api/whip`
4. Stream Key: leave blank (or set `STREAM_KEY` in `.env`)
5. Output → Encoder: **H.264** (x264)
6. Click **Start Streaming**

See `scripts/configure-obs.md` for detailed instructions.

## Configuration

All settings via environment variables (see `.env.example`):

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP server port |
| `UDP_PORT_MIN` | `3000` | ICE UDP port range start |
| `UDP_PORT_MAX` | `4000` | ICE UDP port range end |
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
