// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

// Package connect wires the `veil connect` subcommand: load a client
// configuration, dial each configured server endpoint in order until
// one succeeds, run the Noise XK initiator handshake, then expose a
// local SOCKS5 listener that forwards each accepted connection
// through the established Veil session.
package connect

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/urfave/cli/v3"

	"github.com/redstone-md/veil/core/internal/config"
	"github.com/redstone-md/veil/core/internal/crypto"
	"github.com/redstone-md/veil/core/internal/dpi/decoy"
	"github.com/redstone-md/veil/core/internal/dpi/snipool"
	"github.com/redstone-md/veil/core/internal/dpi/utlsdial"
	"github.com/redstone-md/veil/core/internal/proxy"
	"github.com/redstone-md/veil/core/internal/session"
	"github.com/redstone-md/veil/core/internal/transport"
	"github.com/redstone-md/veil/core/internal/transport/quictr"
	"github.com/redstone-md/veil/core/internal/transport/realitytr"
	"github.com/redstone-md/veil/core/internal/transport/wsstr"
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

	fb := transport.NewFallback(slog.Default())
	for i, s := range cfg.Servers {
		d, err := buildDialer(s, serverPub)
		if err != nil {
			return fmt.Errorf("client.servers[%d]: %w", i, err)
		}
		fb.Add(string(s.Type), s.Addr, d)
	}

	conn, label, err := fb.Dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	slog.Info("transport connected",
		"transport", label,
		"remote", conn.RemoteAddr().String(),
	)

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

	if cfg.Decoy.Enabled {
		startDecoyEngine(ctx, cfg.Decoy, staticKP.Public)
	}

	slog.Info("ready", "socks5", socksAddr, "transport", label)

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

// startDecoyEngine spins up the cover-traffic generator in a
// background goroutine. The engine is best-effort: if it cannot
// reach any of the SNI pool entries it logs at debug and keeps
// trying. It does not block the SOCKS5 path.
func startDecoyEngine(ctx context.Context, dc config.DecoyConfig, userKey []byte) {
	pool := snipool.New()
	region := snipool.Region(dc.Region)
	fp := utlsdial.Fingerprint(dc.Fingerprint)
	if fp == "" {
		fp = utlsdial.FingerprintChromeAuto
	}
	eng := decoy.New(pool, decoy.Config{
		Region:      region,
		UserKey:     string(userKey),
		ShardSize:   dc.ShardSize,
		Concurrency: dc.Concurrency,
		IntervalMS:  dc.IntervalMS,
		Fingerprint: fp,
	}, slog.Default())
	go func() {
		if err := eng.Run(ctx); err != nil {
			slog.Warn("decoy engine stopped", "err", err)
		}
	}()
}

func buildDialer(s config.ClientServer, serverStaticPub []byte) (transport.Dialer, error) {
	switch s.Type {
	case config.TransportQUIC:
		return quictr.NewDialer(), nil
	case config.TransportWSS:
		dc := wsstr.DialConfig{
			SNI:                s.SNI,
			Path:               s.Path,
			InsecureSkipVerify: s.InsecureSkipVerify(),
		}
		if s.Fingerprint != "off" {
			fp := utlsdial.FingerprintChromeAuto
			if s.Fingerprint != "" {
				fp = utlsdial.Fingerprint(s.Fingerprint)
			}
			dc.TLSDial = func(ctx context.Context, network, addr, sni string) (net.Conn, error) {
				return utlsdial.Dial(ctx, network, addr, utlsdial.Options{
					Fingerprint:        fp,
					SNI:                sni,
					InsecureSkipVerify: s.InsecureSkipVerify(),
					NextProtos:         []string{"http/1.1"},
				})
			}
		}
		return wsstr.NewDialer(dc), nil
	case config.TransportReality:
		secret, err := realitytr.DeriveAuthSecret(serverStaticPub)
		if err != nil {
			return nil, err
		}
		return realitytr.NewDialer(realitytr.DialConfig{
			Secret: secret,
			SNI:    s.SNI,
		}), nil
	default:
		return nil, fmt.Errorf("unknown transport type %q", s.Type)
	}
}
