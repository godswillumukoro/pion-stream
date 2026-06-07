# Configuring OBS Studio for WHIP Streaming

This guide walks through configuring OBS Studio to publish to pion-stream
using the WHIP protocol.

---

## Step 1: Open Stream Settings

In OBS Studio, go to **Settings → Stream** (or click the **Settings** button
in the Controls dock, then select **Stream** from the sidebar).

---

## Step 2: Configure Stream Service

- **Service:** Select **Custom...** from the dropdown
- **Server:** Enter your pion-stream WHIP endpoint:
  ```
  http://YOUR_VPS_IP/api/whip
  ```
- **Stream Key:** Leave blank unless you set a `STREAM_KEY` in your `.env`
  file. If you did set one, enter it here.

> **Note:** OBS 30.0+ has native WHIP support. For older versions, you may
> need to use a custom WHIP output plugin. Check your OBS version under
> **Help → About**.

---

## Step 3: Configure Output (Encoder)

Go to **Settings → Output** and set the **Output Mode** to **Advanced**.

### Streaming Tab

| Setting | Value |
|---|---|
| **Encoder** | `x264` (software) or a hardware H.264 encoder |
| **Rate Control** | `CBR` (Constant Bitrate) |
| **Bitrate** | `2500–6000 Kbps` (adjust for your upload bandwidth) |
| **Keyframe Interval** | `2` seconds |
| **CPU Usage Preset** | `veryfast` or `faster` |
| **Profile** | `baseline` or `main` |
| **Tune** | `zerolatency` |

> **Why H.264?** WebRTC requires H.264 or VP8/VP9 for browser compatibility.
> H.264 offers the broadest support across browsers and devices. pion-stream
> does not transcode — the codec you send is the codec viewers receive.

### Audio Tab

| Setting | Value |
|---|---|
| **Audio Bitrate** | `128–160 Kbps` |
| **Sample Rate** | `48 kHz` |

---

## Step 4: Start Streaming

1. Click **OK** to close Settings
2. Click **Start Streaming** in the Controls dock

You should see the status bar at the bottom of OBS show a green square
indicating the stream is live. The pion-stream viewer page at
`http://YOUR_VPS_IP` should now show **LIVE** with the video playing.

---

## Troubleshooting

### Stream won't start / OBS shows "Failed to connect"

1. **Check the server is running:**
   ```bash
   curl http://YOUR_VPS_IP/health
   # Should return: OK
   ```

2. **Check firewall ports:**
   ```bash
   sudo ufw status
   # Should show: 80/tcp ALLOW, 3000/udp ALLOW
   ```

3. **Check server logs:**
   ```bash
   journalctl -u pion-stream -f
   ```

4. **Verify encoder:** Make sure you're using H.264. HEVC/H.265 is not
   supported by most browsers via WebRTC.

### Stream starts but browser shows "Offline" or no video

1. **Wait 5–10 seconds** — ICE negotiation can take a moment
2. **Check browser console** (F12) for WebRTC errors
3. **Verify PUBLIC_IP** is set correctly in `.env` if behind NAT
4. **Try a different browser** — Safari has stricter WebRTC policies

### High latency (> 1 second)

- Lower the keyframe interval to 2 seconds (see Step 3)
- Reduce bitrate if your network is constrained
- Check `tune=zerolatency` is set in the encoder settings
