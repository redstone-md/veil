// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

// Package connect wires the `veil connect` subcommand to the
// embeddable client in internal/client.
package connect

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/redstone-md/veil/core/internal/client"
	"github.com/redstone-md/veil/core/internal/config"
	"github.com/redstone-md/veil/core/internal/sharelink"
)

// Command returns the `veil connect` cli.Command.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "connect",
		Usage: "Connect to a Veil server and expose a local SOCKS5 proxy",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "Path to the client YAML configuration file",
			},
			&cli.StringFlag{
				Name:    "link",
				Aliases: []string{"l"},
				Usage:   "veil:// share link (alternative to --config)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfgPath := cmd.String("config")
			link := cmd.String("link")
			if cfgPath == "" && link == "" {
				return errors.New("connect: --config or --link is required")
			}
			if cfgPath != "" && link != "" {
				return errors.New("connect: --config and --link are mutually exclusive")
			}
			return run(ctx, cfgPath, link)
		},
	}
}

func run(ctx context.Context, cfgPath, link string) error {
	var cfg *config.ClientConfig
	switch {
	case cfgPath != "":
		c, err := config.LoadClient(cfgPath)
		if err != nil {
			return err
		}
		cfg = c
	case link != "":
		c, err := sharelink.Decode(link)
		if err != nil {
			return fmt.Errorf("decode link: %w", err)
		}
		if err := c.Validate(); err != nil {
			return err
		}
		cfg = c
		// Without an explicit config path, default the static key
		// into the user's working directory.
		if cfg.StaticKeyPath == "" {
			cfg.StaticKeyPath = "veil-client.key"
		}
	}

	logger := slog.Default()
	cli := client.New(cfg, logger, client.ListenerFunc(func(e client.Event) {
		// At the CLI level events surface as info-level slog
		// records; the structured fields make them grep-friendly.
		logger.Info("event", "type", e.Type, "msg", e.Message,
			"transport", e.Transport, "remote", e.Remote)
	}))
	if err := cli.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "veil:", err)
		return err
	}
	return nil
}
