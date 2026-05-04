// Veil VPN — Fly.io edge worker.
//
// A small Go HTTP server that accepts WSS upgrades on a configured
// path and proxies the binary websocket frames to an origin Veil
// server over a raw TCP connection. Functionally equivalent to the
// Deno Deploy worker in deploy/edge/deno/, but runs as a container
// on Fly's edge regions instead of Deno's free tier.
//
// Configuration is exclusively via environment variables so the
// fly.toml stays the single source of truth:
//
//	VEIL_LISTEN          host:port to bind (default ":8080")
//	VEIL_ORIGIN_HOST     IP or hostname of the origin VPS (required)
//	VEIL_ORIGIN_PORT     origin's WSS-listener port (default 443)
//	VEIL_PATH            URL path that accepts upgrades (default "/ws")
//
// The worker handles AEAD-encrypted ciphertext only; see
// docs/architecture/ADR-0004-edge-backends.md for the trust model.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

const (
	defaultListen     = ":8080"
	defaultOriginPort = "443"
	defaultPath       = "/ws"
	dialTimeout       = 15 * time.Second
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := loadConfig()
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		// 404 every non-WS request so the deployment does not
		// self-fingerprint.
		http.Error(w, "Not Found", http.StatusNotFound)
	})
	mux.HandleFunc(cfg.Path, makeHandler(cfg, logger))

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: dialTimeout,
		IdleTimeout:       90 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info("listening",
		"addr", cfg.Listen, "path", cfg.Path,
		"origin", net.JoinHostPort(cfg.OriginHost, cfg.OriginPort))

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serve", "err", err)
		os.Exit(1)
	}
}

type config struct {
	Listen     string
	OriginHost string
	OriginPort string
	Path       string
}

func loadConfig() (*config, error) {
	host := os.Getenv("VEIL_ORIGIN_HOST")
	if host == "" {
		return nil, fmt.Errorf("VEIL_ORIGIN_HOST is required")
	}
	c := &config{
		Listen:     defaultIfEmpty(os.Getenv("VEIL_LISTEN"), defaultListen),
		OriginHost: host,
		OriginPort: defaultIfEmpty(os.Getenv("VEIL_ORIGIN_PORT"), defaultOriginPort),
		Path:       defaultIfEmpty(os.Getenv("VEIL_PATH"), defaultPath),
	}
	if !strings.HasPrefix(c.Path, "/") {
		c.Path = "/" + c.Path
	}
	return c, nil
}

func defaultIfEmpty(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func makeHandler(cfg *config, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true, // origin checks are not meaningful here
			Subprotocols:       []string{"binary"},
		})
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")

		dialCtx, dialCancel := context.WithTimeout(r.Context(), dialTimeout)
		origin, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(
			dialCtx, "tcp", net.JoinHostPort(cfg.OriginHost, cfg.OriginPort))
		dialCancel()
		if err != nil {
			logger.Warn("origin dial", "err", err)
			_ = c.Close(websocket.StatusInternalError, "origin dial failed")
			return
		}
		defer origin.Close()

		netConn := websocket.NetConn(r.Context(), c, websocket.MessageBinary)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = io.Copy(origin, netConn)
			if tc, ok := origin.(*net.TCPConn); ok {
				_ = tc.CloseWrite()
			}
		}()
		go func() {
			defer wg.Done()
			_, _ = io.Copy(netConn, origin)
		}()
		wg.Wait()
	}
}
