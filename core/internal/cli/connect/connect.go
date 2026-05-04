// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

// Package connect wires the `veil connect` subcommand: load a client
// configuration, dial the configured server over QUIC, run the
// Noise XK initiator handshake, then expose a local SOCKS5 listener
// that forwards each accepted connection through the established
// Veil session.
package connect

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/urfave/cli/v3"

	"github.com/redstone-md/veil/core/internal/config"
	"github.com/redstone-md/veil/core/internal/crypto"
	"github.com/redstone-md/veil/core/internal/proxy"
	"github.com/redstone-md/veil/core/internal/session"
	"github.com/redstone-md/veil/core/internal/transport/quictr"
)

// Command returns the `veil connect` cli.Command.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "connect",
		Usage: "Connect to a Veil server and expose a local SOCKS5 proxy",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "config",
				Aliases:  []string{"c"},
				Usage:    "Path to the client YAML configuration file",
				Required: true,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return run(ctx, cmd.String("config"))
		},
	}
}

func run(ctx context.Context, cfgPath string) error {
	cfg, err := config.LoadClient(cfgPath)
	if err != nil {
		return err
	}
	socksAddr := cfg.SOCKS5Listen
	if socksAddr == "" {
		socksAddr = "127.0.0.1:1080"
	}

	staticKP, err := crypto.LoadOrCreateKeypair(cfg.StaticKeyPath)
	if err != nil {
		return err
	}
	slog.Info("client static key ready",
		"public_key_b64", crypto.EncodePublicKey(staticKP.Public),
		"hint", "add this line to the server's authorized_keys file",
	)

	serverPub, err := crypto.DecodePublicKey(cfg.ServerStaticKeyB64)
	if err != nil {
		return fmt.Errorf("server static key: %w", err)
	}

	conn, err := quictr.NewDialer().Dial(ctx, cfg.ServerAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	slog.Info("transport connected", "transport", "quic", "remote", conn.RemoteAddr().String())

	established, err := session.HandshakeAsInitiator(conn, *staticKP, serverPub)
	if err != nil {
		return fmt.Errorf("handshake: %w", err)
	}
	slog.Info("session established")

	secure := session.NewSecureChannel(conn, established)
	sess := session.New(secure, session.Options{Role: session.RoleClient})

	runErr := make(chan error, 1)
	go func() { runErr <- sess.Run() }()

	socks := proxy.NewSOCKS5(sess, slog.Default())
	socksErr := make(chan error, 1)
	go func() { socksErr <- socks.ListenAndServe(ctx, socksAddr) }()

	slog.Info("ready", "socks5", socksAddr, "tunnel", cfg.ServerAddr)

	select {
	case err := <-runErr:
		_ = sess.Close()
		if err != nil {
			return fmt.Errorf("session: %w", err)
		}
		return nil
	case err := <-socksErr:
		_ = sess.Close()
		if err != nil {
			return fmt.Errorf("socks5: %w", err)
		}
		return nil
	case <-ctx.Done():
		_ = sess.Close()
		return nil
	}
}
