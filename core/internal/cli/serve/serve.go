// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

// Package serve wires the `veil serve` subcommand: load server
// configuration, listen for QUIC connections, run a Noise XK
// responder handshake on each, then run a multiplexed VWP/1
// session that forwards every accepted stream to its requested
// upstream target.
package serve

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/redstone-md/veil/core/internal/config"
	"github.com/redstone-md/veil/core/internal/crypto"
	"github.com/redstone-md/veil/core/internal/forward"
	"github.com/redstone-md/veil/core/internal/session"
	"github.com/redstone-md/veil/core/internal/transport"
	"github.com/redstone-md/veil/core/internal/transport/quictr"
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

	ln, err := quictr.Listen(cfg.Listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	slog.Info("listening", "transport", "quic", "addr", cfg.Listen)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Error("accept failed", "err", err)
			continue
		}
		go handleConn(ctx, conn, *staticKP, authorized)
	}
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
