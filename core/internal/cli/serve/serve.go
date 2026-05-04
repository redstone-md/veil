// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

// Package serve wires the `veil serve` subcommand: load server
// configuration, bring up every configured transport listener,
// run the Noise XK responder handshake on each accepted connection,
// then drive a multiplexed VWP/1 session that forwards every
// accepted stream to its requested upstream target.
package serve

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/redstone-md/veil/core/internal/config"
	"github.com/redstone-md/veil/core/internal/crypto"
	"github.com/redstone-md/veil/core/internal/forward"
	"github.com/redstone-md/veil/core/internal/session"
	"github.com/redstone-md/veil/core/internal/transport"
	"github.com/redstone-md/veil/core/internal/transport/quictr"
	"github.com/redstone-md/veil/core/internal/transport/wsstr"
)

// Command returns the `veil serve` cli.Command.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "serve",
		Usage: "Run a Veil server",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "config",
				Aliases:  []string{"c"},
				Usage:    "Path to the server YAML configuration file",
				Required: true,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return run(ctx, cmd.String("config"))
		},
	}
}

func run(ctx context.Context, cfgPath string) error {
	cfg, err := config.LoadServer(cfgPath)
	if err != nil {
		return err
	}

	staticKP, err := crypto.LoadOrCreateKeypair(cfg.StaticKeyPath)
	if err != nil {
		return err
	}
	slog.Info("server static key ready",
		"path", cfg.StaticKeyPath,
		"public_key_b64", crypto.EncodePublicKey(staticKP.Public),
	)

	authorized, err := loadAuthorizedKeys(cfg.AuthorizedKeysPath)
	if err != nil {
		return err
	}
	slog.Info("authorized client keys loaded", "count", len(authorized))

	fanIn := transport.NewFanIn(slog.Default())
	for i, t := range cfg.Transports {
		ln, err := buildListener(t)
		if err != nil {
			return fmt.Errorf("transport[%d] %s: %w", i, t.Type, err)
		}
		label := fmt.Sprintf("%s@%s", t.Type, t.Listen)
		fanIn.Add(label, ln)
		slog.Info("listening", "transport", t.Type, "addr", t.Listen)
	}
	defer fanIn.Close()

	go func() {
		<-ctx.Done()
		_ = fanIn.Close()
	}()

	for {
		conn, err := fanIn.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Error("accept failed", "err", err)
			return err
		}
		go handleConn(ctx, conn, *staticKP, authorized)
	}
}

func buildListener(t config.ServerTransport) (transport.Listener, error) {
	switch t.Type {
	case config.TransportQUIC:
		return quictr.Listen(t.Listen)
	case config.TransportWSS:
		tlsCfg, err := buildServerTLS(t)
		if err != nil {
			return nil, err
		}
		return wsstr.Listen(wsstr.ListenConfig{
			Addr: t.Listen,
			Path: t.Path,
			TLS:  tlsCfg,
		})
	case config.TransportReality:
		return nil, fmt.Errorf("reality transport not yet implemented; see docs/architecture/ADR-0002")
	default:
		return nil, fmt.Errorf("unknown transport type %q", t.Type)
	}
}

func buildServerTLS(t config.ServerTransport) (*tls.Config, error) {
	if t.CertFile != "" && t.KeyFile != "" {
		return wsstr.LoadTLSConfig(t.CertFile, t.KeyFile)
	}
	host, _, err := net.SplitHostPort(t.Listen)
	if err != nil {
		host = "localhost"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	slog.Warn("self-signed TLS cert in use",
		"transport", t.Type,
		"hint", "set cert_file and key_file for production",
		"cn", host,
	)
	return wsstr.SelfSignedTLSConfig(host)
}

func handleConn(ctx context.Context, conn transport.Conn, staticKP crypto.Keypair, authorized map[string]struct{}) {
	defer conn.Close()
	logger := slog.With("peer", conn.RemoteAddr().String())

	established, err := session.HandshakeAsResponder(conn, staticKP)
	if err != nil {
		logger.Warn("handshake failed", "err", err)
		return
	}
	peerB64 := base64.StdEncoding.EncodeToString(established.PeerStatic)
	if _, ok := authorized[peerB64]; !ok {
		logger.Warn("unauthorized client", "client_pubkey_b64", peerB64)
		return
	}
	logger.Info("client authenticated", "client_pubkey_b64", peerB64)

	secure := session.NewSecureChannel(conn, established)
	sess := session.New(secure, session.Options{Role: session.RoleServer, Logger: logger})

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	runErr := make(chan error, 1)
	go func() { runErr <- sess.Run() }()

	fwd := forward.NewServer(sess, logger, nil)
	fwdErr := make(chan error, 1)
	go func() { fwdErr <- fwd.Run(connCtx) }()

	select {
	case err := <-runErr:
		if err != nil {
			logger.Info("session ended", "err", err)
		} else {
			logger.Info("session ended cleanly")
		}
	case err := <-fwdErr:
		if err != nil {
			logger.Info("forward ended", "err", err)
		}
	case <-ctx.Done():
	}
	_ = sess.Close()
}

func loadAuthorizedKeys(path string) (map[string]struct{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read authorized keys: %w", err)
	}
	out := make(map[string]struct{})
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, err := base64.StdEncoding.DecodeString(line); err != nil {
			return nil, fmt.Errorf("authorized_keys line %d: invalid base64: %w", i+1, err)
		}
		out[line] = struct{}{}
	}
	return out, nil
}
