#!/bin/bash
#
# pion-stream deployment script for Ubuntu 22.04 LTS
# ====================================================
# Idempotent — safe to run multiple times. Installs Go (if needed),
# clones/updates the repository, builds the binary, configures the
# firewall, and sets up the systemd service.
#
# Usage:
#   chmod +x scripts/setup.sh
#   sudo ./scripts/setup.sh
#
# Or via make:
#   make deploy HOST=<ip>

set -euo pipefail

REPO_URL="https://github.com/godswillumukoro/pion-stream"
INSTALL_DIR="/opt/pion-stream"
GO_VERSION="1.22.0"
SERVICE_NAME="pion-stream"

echo "=== pion-stream setup ==="
echo ""

# ── 1. Install Go if not present ──────────────────────────────────

install_go() {
    if command -v go &>/dev/null; then
        local current
        current=$(go version | grep -oP 'go\K[0-9]+\.[0-9]+')
        echo "[✓] Go $current already installed"
        return
    fi

    echo "[…] Installing Go ${GO_VERSION} ..."
    local arch
    arch=$(uname -m)
    if [ "$arch" = "x86_64" ]; then
        arch="amd64"
    elif [ "$arch" = "aarch64" ]; then
        arch="arm64"
    else
        echo "[✗] Unsupported architecture: $arch"
        exit 1
    fi

    local tarball="go${GO_VERSION}.linux-${arch}.tar.gz"
    wget -q "https://go.dev/dl/${tarball}" -O /tmp/${tarball}
    rm -rf /usr/local/go
    tar -C /usr/local -xzf /tmp/${tarball}
    rm /tmp/${tarball}

    # Add Go to PATH for this session and future logins.
    export PATH="/usr/local/go/bin:$PATH"
    if ! grep -q '/usr/local/go/bin' /etc/profile.d/go.sh 2>/dev/null; then
        echo 'export PATH="/usr/local/go/bin:$PATH"' > /etc/profile.d/go.sh
    fi

    echo "[✓] Go ${GO_VERSION} installed"
}

# ── 2. Clone or pull the repository ───────────────────────────────

clone_or_pull() {
    if [ -d "${INSTALL_DIR}/.git" ]; then
        echo "[…] Updating existing repository ..."
        cd "${INSTALL_DIR}"
        git fetch origin
        git reset --hard origin/main
        echo "[✓] Repository updated"
    else
        echo "[…] Cloning repository ..."
        git clone "${REPO_URL}" "${INSTALL_DIR}"
        echo "[✓] Repository cloned to ${INSTALL_DIR}"
    fi
}

# ── 3. Build the binary ───────────────────────────────────────────

build_binary() {
    echo "[…] Building streaming-server ..."
    cd "${INSTALL_DIR}"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
        -ldflags="-s -w" \
        -o streaming-server \
        ./server/
    chmod +x streaming-server
    echo "[✓] Binary built: $(file streaming-server)"
}

# ── 4. Configure firewall ─────────────────────────────────────────

configure_firewall() {
    if ! command -v ufw &>/dev/null; then
        echo "[!] ufw not found — skipping firewall configuration"
        return
    fi

    echo "[…] Configuring firewall ..."
    ufw allow 80/tcp comment 'pion-stream HTTP' || true
    ufw allow 3000/udp comment 'pion-stream WebRTC media' || true
    ufw --force enable || true
    echo "[✓] Firewall configured"
}

# ── 5. Set up systemd service ─────────────────────────────────────

setup_systemd() {
    echo "[…] Installing systemd service ..."
    cp "${INSTALL_DIR}/systemd/${SERVICE_NAME}.service" \
       "/etc/systemd/system/${SERVICE_NAME}.service"

    # Create .env file from example if it doesn't exist.
    if [ ! -f "${INSTALL_DIR}/.env" ]; then
        cp "${INSTALL_DIR}/.env.example" "${INSTALL_DIR}/.env"
        echo "[!] Created .env from .env.example — edit if needed:"
        echo "    nano ${INSTALL_DIR}/.env"
    fi

    systemctl daemon-reload
    systemctl enable "${SERVICE_NAME}"
    systemctl restart "${SERVICE_NAME}"
    echo "[✓] Systemd service installed and started"
}

# ── 6. Verify ─────────────────────────────────────────────────────

verify() {
    echo ""
    echo "[…] Verifying deployment ..."
    sleep 2

    if systemctl is-active --quiet "${SERVICE_NAME}"; then
        echo "[✓] Service is running"
    else
        echo "[✗] Service failed to start — check: journalctl -u ${SERVICE_NAME} -n 50"
        return
    fi

    if curl -sf http://localhost/health > /dev/null 2>&1; then
        echo "[✓] Health check passed (http://localhost/health)"
    else
        echo "[✗] Health check failed"
    fi
}

# ── Main ──────────────────────────────────────────────────────────

install_go
clone_or_pull
build_binary
configure_firewall
setup_systemd
verify

# Display the public IP if available.
PUBLIC_IP=$(curl -sf http://checkip.amazonaws.com 2>/dev/null || \
            curl -sf http://ifconfig.me 2>/dev/null || \
            echo "YOUR_SERVER_IP")

echo ""
echo "================================================"
echo "  pion-stream is live!"
echo ""
echo "  Viewer:  http://${PUBLIC_IP}"
echo "  Health:  http://${PUBLIC_IP}/health"
echo "  Status:  systemctl status pion-stream"
echo "  Logs:    journalctl -u pion-stream -f"
echo "================================================"
