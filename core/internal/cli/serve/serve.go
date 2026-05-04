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
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/redstone-md/veil/core/internal/auth"
	"github.com/redstone-md/veil/core/internal/config"
	"github.com/redstone-md/veil/core/internal/crypto"
	"github.com/redstone-md/veil/core/internal/forward"
	"github.com/redstone-md/veil/core/internal/session"
	"github.com/redstone-md/veil/core/internal/transport"
	"github.com/redstone-md/veil/core/internal/transport/quictr"
	"github.com/redstone-md/veil/core/internal/transport/realitytr"
	"github.com/redstone-md/veil/core/internal/transport/wsstr"
	"github.com/redstone-md/veil/core/internal/users"
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

	authn, store, err := buildAuthenticator(cfg)
	if err != nil {
		return err
	}
	if store != nil {
		defer store.Close()
	}

	fanIn := transport.NewFanIn(slog.Default())
	for i, t := range cfg.Transports {
		ln, err := buildListener(t, staticKP.Public)
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
		go handleConn(ctx, conn, *staticKP, authn)
	}
}

func buildAuthenticator(cfg *config.ServerConfig) (auth.Authenticator, *users.Store, error) {
	if cfg.UserDBPath != "" {
		store, err := users.Open(cfg.UserDBPath)
		if err != nil {
			return nil, nil, err
		}
		count, _ := store.CountActive(context.Background())
		slog.Info("user store opened", "path", cfg.UserDBPath, "active_users", count)
		return auth.NewStoreBackend(store), store, nil
	}
	if cfg.AuthorizedKeysPath != "" {
		fb, err := auth.LoadFile(cfg.AuthorizedKeysPath)
		if err != nil {
			return nil, nil, err
		}
		slog.Info("authorized_keys loaded", "path", fb.Path(), "count", fb.Count())
		return fb, nil, nil
	}
	return nil, nil, errors.New("no authenticator configured")
}

func buildListener(t config.ServerTransport, serverStaticPub []byte) (transport.Listener, error) {
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
		secret, err := realitytr.DeriveAuthSecret(serverStaticPub)
		if err != nil {
			return nil, err
		}
		return realitytr.Listen(realitytr.ListenConfig{
			Addr:       t.Listen,
			Secret:     secret,
			TargetSNI:  t.TargetSNI,
			TargetAddr: t.TargetAddr,
			Logger:     slog.Default(),
		})
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

func handleConn(ctx context.Context, conn transport.Conn, staticKP crypto.Keypair, authn auth.Authenticator) {
	defer conn.Close()
	logger := slog.With("peer", conn.RemoteAddr().String())

	established, err := session.HandshakeAsResponder(conn, staticKP)
	if err != nil {
		logger.Warn("handshake failed", "err", err)
		return
	}
	peerB64 := base64.StdEncoding.EncodeToString(established.PeerStatic)
	res, err := authn.Verify(ctx, peerB64)
	if err != nil {
		logger.Warn("auth rejected",
			"client_pubkey_b64", peerB64, "err", err)
		return
	}
	logger = logger.With("user", res.Name)
	if res.UserID != "" {
		logger = logger.With("user_id", res.UserID)
	}
	logger.Info("client authenticated")

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

// _ keeps the os import used even if the file is later trimmed.
var _ = os.Args
