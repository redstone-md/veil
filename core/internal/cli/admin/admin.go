// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

// Package admin wires the `veil admin` CLI subcommand: it serves
// the embedded admin HTTP/API on a configurable address (default
// 127.0.0.1:8443) and exposes admin-user management as nested
// subcommands.
package admin

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/redstone-md/veil/core/internal/admin"
	"github.com/redstone-md/veil/core/internal/config"
	"github.com/redstone-md/veil/core/internal/crypto"
	"github.com/redstone-md/veil/core/internal/users"
)

// Command returns the `veil admin` cli.Command tree.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "admin",
		Usage: "Run or manage the embedded admin HTTP server",
		Commands: []*cli.Command{
			serveCmd(),
			userCreateCmd(),
			userPasswdCmd(),
		},
	}
}

func dbFlag() cli.Flag {
	return &cli.StringFlag{
		Name:    "db",
		Aliases: []string{"d"},
		Usage:   "Path to the user database (SQLite)",
		Sources: cli.EnvVars("VEIL_USER_DB"),
		Value:   "veil-users.db",
	}
}

func serveCmd() *cli.Command {
	return &cli.Command{
		Name:  "serve",
		Usage: "Start the admin HTTP server",
		Flags: []cli.Flag{
			dbFlag(),
			&cli.StringFlag{
				Name:  "addr",
				Usage: "host:port to bind",
				Value: "127.0.0.1:8443",
			},
			&cli.StringFlag{
				Name:  "server-config",
				Usage: "Path to the running veil server's YAML config. When set, /api/server-info exposes the server's static pubkey + transport list so installers / GUI clients can assemble share-links without operator copy-paste.",
			},
			&cli.StringFlag{
				Name:  "public-host",
				Usage: "Public hostname or IP that clients reach the server at. Required with --server-config when transport listens are :PORT (no host).",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			s, err := users.Open(cmd.String("db"))
			if err != nil {
				return err
			}
			defer s.Close()

			n, _ := s.CountAdmins(ctx)
			if n == 0 {
				return errors.New("admin: no admin users defined; run `veil admin user-create` first")
			}

			var info *admin.ServerInfo
			if path := cmd.String("server-config"); path != "" {
				info, err = loadServerInfo(path, cmd.String("public-host"))
				if err != nil {
					return fmt.Errorf("admin: --server-config: %w", err)
				}
			}

			srv, err := admin.New(admin.Config{
				Addr:       cmd.String("addr"),
				Store:      s,
				ServerInfo: info,
			})
			if err != nil {
				return err
			}
			return srv.Run(ctx)
		},
	}
}

// loadServerInfo parses the running server's YAML config + static
// keypair so /api/server-info can hand the data to GUI clients.
func loadServerInfo(yamlPath, publicHost string) (*admin.ServerInfo, error) {
	cfg, err := config.LoadServer(yamlPath)
	if err != nil {
		return nil, err
	}
	kp, err := crypto.LoadOrCreateKeypair(cfg.StaticKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read static key %q: %w", cfg.StaticKeyPath, err)
	}
	info := &admin.ServerInfo{
		StaticPubkeyB64: base64.StdEncoding.EncodeToString(kp.Public),
	}
	for _, t := range cfg.Transports {
		listens := t.Listens
		if len(listens) == 0 {
			listens = []string{t.Listen}
		}
		for _, l := range listens {
			addr := normaliseListen(l, publicHost)
			info.Transports = append(info.Transports, admin.TransportInfo{
				Type: string(t.Type),
				Addr: addr,
				SNI:  t.TargetSNI,
				Path: t.Path,
			})
		}
	}
	return info, nil
}

// normaliseListen swaps a wildcard listen ("0.0.0.0:443" / ":443")
// for the publicly-reachable host so share-links are usable without
// further hand-editing.
func normaliseListen(listen, publicHost string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		if publicHost != "" {
			return net.JoinHostPort(publicHost, port)
		}
	}
	return listen
}

func userCreateCmd() *cli.Command {
	return &cli.Command{
		Name:  "user-create",
		Usage: "Create a new admin login",
		Flags: []cli.Flag{
			dbFlag(),
			&cli.StringFlag{Name: "username", Required: true},
			&cli.StringFlag{Name: "password", Usage: "Password (omit for interactive prompt)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			s, err := users.Open(cmd.String("db"))
			if err != nil {
				return err
			}
			defer s.Close()
			pw := cmd.String("password")
			if pw == "" {
				p, err := promptPassword("Password: ")
				if err != nil {
					return err
				}
				pw = p
			}
			if len(pw) < 8 {
				return errors.New("password too short (>=8)")
			}
			if err := s.CreateAdminUser(ctx, cmd.String("username"), pw); err != nil {
				return err
			}
			fmt.Printf("admin user %q ready\n", cmd.String("username"))
			return nil
		},
	}
}

func userPasswdCmd() *cli.Command {
	return &cli.Command{
		Name:  "user-passwd",
		Usage: "Change an admin login's password",
		Flags: []cli.Flag{
			dbFlag(),
			&cli.StringFlag{Name: "username", Required: true},
			&cli.StringFlag{Name: "password", Usage: "New password (omit for interactive prompt)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			s, err := users.Open(cmd.String("db"))
			if err != nil {
				return err
			}
			defer s.Close()
			pw := cmd.String("password")
			if pw == "" {
				p, err := promptPassword("New password: ")
				if err != nil {
					return err
				}
				pw = p
			}
			if len(pw) < 8 {
				return errors.New("password too short (>=8)")
			}
			// CreateAdminUser is upsert via ON CONFLICT DO UPDATE.
			if err := s.CreateAdminUser(ctx, cmd.String("username"), pw); err != nil {
				return err
			}
			fmt.Printf("password updated for %q\n", cmd.String("username"))
			return nil
		},
	}
}

func promptPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	b, err := term.ReadPassword(0) // stdin fd
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(b), nil
}
