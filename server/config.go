// Command pion-stream is a self-hosted live streaming server built on
// Pion WebRTC. It accepts WHIP publishes from OBS and serves WHEP streams
// to browser-based viewers with a minimal HTMX-powered UI.
package main

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all runtime configuration for the streaming server.
// Values are loaded from environment variables with sensible defaults.
type Config struct {
	// Port is the HTTP server listen port (default: 8080).
	Port int

	// UDPPortMin is the start of the ICE UDP candidate port range.
	UDPPortMin int

	// UDPPortMax is the end of the ICE UDP candidate port range.
	UDPPortMax int

	// PublicIP is the server's public IP, used for ICE host candidates.
	// Leave empty if the server has a public interface directly.
	PublicIP string

	// STUNServer is the STUN server URL for NAT traversal.
	STUNServer string

	// StreamKey is an optional bearer token for WHIP publish authentication.
	// If empty, no authentication is required.
	StreamKey string
}

// DefaultConfig returns a Config populated with safe defaults.
func DefaultConfig() Config {
	return Config{
		Port:       80,
		UDPPortMin: 3000,
		UDPPortMax: 4000,
		PublicIP:   "",
		STUNServer: "stun:stun.l.google.com:19302",
		StreamKey:  "",
	}
}

// LoadConfig reads configuration from environment variables and returns
// a Config struct. Unset variables fall back to defaults.
func LoadConfig() Config {
	cfg := DefaultConfig()

	if v := os.Getenv("PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Port = p
		}
	}

	if v := os.Getenv("UDP_PORT_MIN"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.UDPPortMin = p
		}
	}

	if v := os.Getenv("UDP_PORT_MAX"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.UDPPortMax = p
		}
	}

	if v := os.Getenv("PUBLIC_IP"); v != "" {
		cfg.PublicIP = v
	}

	if v := os.Getenv("STUN_SERVER"); v != "" {
		cfg.STUNServer = v
	}

	if v := os.Getenv("STREAM_KEY"); v != "" {
		cfg.StreamKey = v
	}

	return cfg
}

// Addr returns the listen address string for the HTTP server.
func (c Config) Addr() string {
	return fmt.Sprintf(":%d", c.Port)
}
