// Package main is the entry point for the pion-stream server.
// It sets up an HTTP server with chi routing, configures WebRTC endpoints
// for WHIP ingest and WHEP egress, and serves an HTMX-powered viewer UI.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
)

func main() {
	// Structured JSON logging to stdout.
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()

	// Load configuration from environment variables.
	cfg := LoadConfig()

	// Create the shared streaming session that coordinates
	// the publisher and all viewer connections.
	session := NewSession(cfg, logger)

	// Build the router with chi.
	router := chi.NewRouter()

	// Standard middleware stack.
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(corsMiddleware)

	// --- Routes ---

	// WHIP ingest endpoint — OBS or any WHIP-compatible client
	// publishes an SDP offer here.
	router.Post("/api/whip", session.HandleWHIP)
	router.Delete("/api/whip", session.HandleWHIPDisconnect)

	// WHIP ingest for browser publishers — separate route for
	// clarity in educational content.
	router.Post("/api/whip/browser", session.HandleWHIPBrowser)
	router.Delete("/api/whip/browser", session.HandleWHIPDelete)

	// WHEP egress endpoint — browser-based viewers request the
	// stream by POSTing their SDP offer.
	router.Post("/api/whep", session.HandleWHEP)

	// Health check for monitoring and uptime verification.
	router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Debug endpoints — inspect SDP and internal state.
	router.Get("/debug/sdp", session.HandleDebugSDP)
	router.Get("/debug/status", session.HandleDebugStatus)

	// Simple test page for WHEP connectivity.
	router.Get("/test", session.HandleTestWHEP)

	// Stream status endpoint — polled by HTMX for live/offline
	// state and viewer count.
	router.Get("/status", session.HandleStatus)

	// Live chat — POST to send, SSE for real-time receive.
	router.Post("/api/chat", session.HandleChatPost)
	router.Get("/api/chat/events", session.HandleChatEvents)

	// Companion landing page — standalone documentation site.
	router.Get("/site", session.HandleSite)

	// Browser studio — publish directly from the browser.
	router.Get("/studio", session.HandleStudio)

	// Viewer UI served at root with embedded HTML template.
	router.Get("/", session.HandleViewer)

	// --- Start server ---

	server := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		logger.Info().
			Int("port", cfg.Port).
			Str("stun_server", cfg.STUNServer).
			Msg("streaming server starting")

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("server failed to start")
		}
	}()

	<-done
	logger.Info().Msg("shutting down server")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Fatal().Err(err).Msg("server forced to shutdown")
	}

	logger.Info().Msg("server stopped")
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
