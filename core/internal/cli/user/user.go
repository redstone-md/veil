// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

// Package user wires the `veil user` subcommand tree, providing
// CRUD-style operations against the embedded SQLite user store.
package user

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"

	"github.com/redstone-md/veil/core/internal/config"
	"github.com/redstone-md/veil/core/internal/crypto"
	"github.com/redstone-md/veil/core/internal/sharelink"
	"github.com/redstone-md/veil/core/internal/users"
)

// Command returns the `veil user` cli.Command (with sub-actions).
func Command() *cli.Command {
	return &cli.Command{
		Name:  "user",
		Usage: "Manage Veil server users",
		Commands: []*cli.Command{
			addCmd(),
			listCmd(),
			revokeCmd(),
			restoreCmd(),
			regenCmd(),
			deleteCmd(),
			showConfigCmd(),
			setQuotaCmd(),
			setExpiryCmd(),
		},
	}
}

// dbFlag is the shared --db flag every user subcommand declares.
// urfave/cli v3.8 does not propagate parent flags into subcommand
// contexts, so we inline the flag at each subcommand instead.
func dbFlag() cli.Flag {
	return &cli.StringFlag{
		Name:    "db",
		Aliases: []string{"d"},
		Usage:   "Path to the user database (SQLite)",
		Sources: cli.EnvVars("VEIL_USER_DB"),
		Value:   "veil-users.db",
	}
}

func openStore(cmd *cli.Command) (*users.Store, error) {
	path := cmd.String("db")
	if path == "" {
		return nil, errors.New("user: --db path is required")
	}
	return users.Open(path)
}

func addCmd() *cli.Command {
	return &cli.Command{
		Name:  "add",
		Usage: "Add a new user",
		Flags: []cli.Flag{
			dbFlag(),
			&cli.StringFlag{Name: "name", Required: true, Usage: "Display name (unique)"},
			&cli.StringFlag{Name: "pubkey", Usage: "Client Noise XK static public key (base64). When omitted, a fresh keypair is generated and the private key is printed once."},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			s, err := openStore(cmd)
			if err != nil {
				return err
			}
			defer s.Close()

			pubB64 := cmd.String("pubkey")
			var generatedPriv []byte
			if pubB64 == "" {
				kp, gerr := crypto.GenerateKeypair()
				if gerr != nil {
					return gerr
				}
				pubB64 = base64.StdEncoding.EncodeToString(kp.Public)
				generatedPriv = kp.Private
			}

			u, err := s.CreateUser(ctx, cmd.String("name"), pubB64)
			if err != nil {
				return err
			}
			fmt.Printf("Created user %s (id=%s).\n", u.Name, u.ID)
			fmt.Printf("Pubkey:  %s\n", pubB64)
			if generatedPriv != nil {
				privB64 := base64.StdEncoding.EncodeToString(generatedPriv)
				fmt.Println()
				fmt.Println("# IMPORTANT: this is the only time the private key is shown.")
				fmt.Println("# The server does NOT keep a copy. Hand it to the user, or fold it")
				fmt.Println("# into a complete share link with `veil user show-config`:")
				fmt.Println()
				fmt.Printf("Privkey: %s\n", privB64)
				fmt.Println()
				fmt.Printf("# Generate a one-step share link:\n")
				fmt.Printf("#   veil user show-config %s \\\n", u.ID)
				fmt.Printf("#     --server-pubkey <SERVER_PUB_B64> \\\n")
				fmt.Printf("#     --server-addr   <HOST:PORT> \\\n")
				fmt.Printf("#     --transport     reality \\\n")
				fmt.Printf("#     --client-key-b64 %s\n", privB64)
			}
			return nil
		},
	}
}

func listCmd() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List all users",
		Flags: []cli.Flag{dbFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			s, err := openStore(cmd)
			if err != nil {
				return err
			}
			defer s.Close()
			all, err := s.ListUsers(ctx)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tID\tSTATUS\tCREATED\tEXPIRES\tQUOTA\tUSED\tLAST_SEEN")
			for _, u := range all {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					u.Name, u.ID, u.Status,
					u.CreatedAt.UTC().Format("2006-01-02"),
					timeOrDash(u.ExpiresAt),
					quotaStr(u.QuotaBytesPerMonth),
					humanBytes(u.UsedBytesCurrentMonth),
					timeOrDash(u.LastSeen),
				)
			}
			return tw.Flush()
		},
	}
}

func revokeCmd() *cli.Command {
	return &cli.Command{
		Name:      "revoke",
		Usage:     "Mark a user as revoked",
		ArgsUsage: "<id>",
		Flags:     []cli.Flag{dbFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			id := cmd.Args().First()
			if id == "" {
				return errors.New("user revoke: <id> is required")
			}
			s, err := openStore(cmd)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.SetStatus(ctx, id, users.StatusRevoked); err != nil {
				return err
			}
			fmt.Printf("revoked user %s\n", id)
			return nil
		},
	}
}

func restoreCmd() *cli.Command {
	return &cli.Command{
		Name:      "restore",
		Usage:     "Mark a previously revoked user as active again",
		ArgsUsage: "<id>",
		Flags:     []cli.Flag{dbFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			id := cmd.Args().First()
			if id == "" {
				return errors.New("user restore: <id> is required")
			}
			s, err := openStore(cmd)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.SetStatus(ctx, id, users.StatusActive); err != nil {
				return err
			}
			fmt.Printf("restored user %s\n", id)
			return nil
		},
	}
}

func regenCmd() *cli.Command {
	return &cli.Command{
		Name:      "regen",
		Usage:     "Replace a user's stored public key",
		ArgsUsage: "<id>",
		Flags: []cli.Flag{
			dbFlag(),
			&cli.StringFlag{Name: "pubkey", Required: true, Usage: "New base64 public key"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			id := cmd.Args().First()
			if id == "" {
				return errors.New("user regen: <id> is required")
			}
			s, err := openStore(cmd)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.RegenKey(ctx, id, cmd.String("pubkey")); err != nil {
				return err
			}
			fmt.Printf("regenerated key for user %s\n", id)
			return nil
		},
	}
}

func deleteCmd() *cli.Command {
	return &cli.Command{
		Name:      "delete",
		Usage:     "Permanently delete a user row",
		ArgsUsage: "<id>",
		Flags:     []cli.Flag{dbFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			id := cmd.Args().First()
			if id == "" {
				return errors.New("user delete: <id> is required")
			}
			s, err := openStore(cmd)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.DeleteUser(ctx, id); err != nil {
				return err
			}
			fmt.Printf("deleted user %s\n", id)
			return nil
		},
	}
}

func setQuotaCmd() *cli.Command {
	return &cli.Command{
		Name:      "set-quota",
		Usage:     "Set per-month byte quota for a user (0 or empty = unlimited)",
		ArgsUsage: "<id>",
		Flags: []cli.Flag{
			dbFlag(),
			&cli.StringFlag{Name: "bytes", Usage: "Quota in bytes (e.g. 5000000000); empty for unlimited"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			id := cmd.Args().First()
			if id == "" {
				return errors.New("user set-quota: <id> is required")
			}
			s, err := openStore(cmd)
			if err != nil {
				return err
			}
			defer s.Close()
			raw := cmd.String("bytes")
			var q *int64
			if raw != "" && raw != "0" {
				v, err := strconv.ParseInt(raw, 10, 64)
				if err != nil {
					return fmt.Errorf("set-quota: parse: %w", err)
				}
				q = &v
			}
			if err := s.SetQuota(ctx, id, q); err != nil {
				return err
			}
			fmt.Printf("set quota for user %s to %s\n", id, quotaStr(q))
			return nil
		},
	}
}

func setExpiryCmd() *cli.Command {
	return &cli.Command{
		Name:      "set-expiry",
		Usage:     "Set expiry timestamp for a user (RFC3339 or empty to clear)",
		ArgsUsage: "<id>",
		Flags: []cli.Flag{
			dbFlag(),
			&cli.StringFlag{Name: "at", Usage: "Expiry time, RFC3339 (e.g. 2026-12-31T23:59:59Z); empty to clear"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			id := cmd.Args().First()
			if id == "" {
				return errors.New("user set-expiry: <id> is required")
			}
			s, err := openStore(cmd)
			if err != nil {
				return err
			}
			defer s.Close()
			raw := cmd.String("at")
			var t *time.Time
			if raw != "" {
				v, err := time.Parse(time.RFC3339, raw)
				if err != nil {
					return fmt.Errorf("set-expiry: parse: %w", err)
				}
				t = &v
			}
			if err := s.SetExpiry(ctx, id, t); err != nil {
				return err
			}
			fmt.Printf("set expiry for user %s to %s\n", id, timeOrDash(t))
			return nil
		},
	}
}

// showConfigCmd emits a ready-to-share client YAML configuration for
// a given user, populated from a server descriptor passed via flags.
func showConfigCmd() *cli.Command {
	return &cli.Command{
		Name:      "show-config",
		Usage:     "Print a ready-to-distribute client YAML config for a user",
		ArgsUsage: "<id>",
		Flags: []cli.Flag{
			dbFlag(),
			&cli.StringFlag{Name: "server-pubkey", Required: true, Usage: "Server's static Noise XK public key (base64)"},
			&cli.StringFlag{Name: "server-addr", Required: true, Usage: "Server host:port"},
			&cli.StringFlag{Name: "transport", Value: "quic", Usage: "Transport type (quic, wss, reality)"},
			&cli.StringFlag{Name: "sni", Usage: "TLS SNI (wss/reality only)"},
			&cli.StringFlag{Name: "path", Usage: "WSS upgrade path"},
			&cli.StringFlag{Name: "fingerprint", Usage: "uTLS fingerprint (chrome/firefox/...)"},
			&cli.StringFlag{Name: "static-key-path", Value: "veil-client.key", Usage: "Where the client should keep its keypair (ignored when --client-key-b64 is set)"},
			&cli.StringFlag{Name: "client-key-b64", Usage: "Embed the client's Noise XK private key inline in the share link. Use the value printed by `veil user add` for a one-shot handoff that does not need an external key file."},
			&cli.StringFlag{Name: "socks5-listen", Value: "127.0.0.1:1080", Usage: "Local SOCKS5 bind address"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			id := cmd.Args().First()
			if id == "" {
				return errors.New("user show-config: <id> is required")
			}
			s, err := openStore(cmd)
			if err != nil {
				return err
			}
			defer s.Close()
			u, err := s.GetUser(ctx, id)
			if err != nil {
				return err
			}
			cfg := config.ClientConfig{
				ServerStaticKeyB64: cmd.String("server-pubkey"),
				SOCKS5Listen:       cmd.String("socks5-listen"),
				Servers: []config.ClientServer{{
					Type:        config.TransportType(cmd.String("transport")),
					Addr:        cmd.String("server-addr"),
					SNI:         cmd.String("sni"),
					Path:        cmd.String("path"),
					Fingerprint: cmd.String("fingerprint"),
				}},
			}
			inline := cmd.String("client-key-b64")
			if inline != "" {
				cfg.StaticKeyInlineB64 = inline
			} else {
				cfg.StaticKeyPath = cmd.String("static-key-path")
			}
			out, err := yaml.Marshal(&cfg)
			if err != nil {
				return err
			}
			link, err := sharelink.Encode(&cfg)
			if err != nil {
				return err
			}
			fmt.Printf("# user: %s (id=%s, status=%s)\n", u.Name, u.ID, u.Status)
			fmt.Printf("# pubkey on file: %s\n", u.PubkeyB64)
			if inline == "" {
				fmt.Printf("# NOTE: this config uses an external key file (--static-key-path).\n")
				fmt.Printf("#       The client's keypair must produce the pubkey above; if not,\n")
				fmt.Printf("#       run `veil user regen --pubkey <new>` after the client connects.\n")
				fmt.Printf("#       For a one-shot handoff use `--client-key-b64 <priv>` instead\n")
				fmt.Printf("#       (the value printed by `veil user add`).\n\n")
			} else {
				fmt.Printf("# inline key: client provisioned in one shot — no key file needed.\n\n")
			}
			os.Stdout.Write(out)
			fmt.Printf("\n# share-link (paste into a client with `veil connect --link` or the desktop's Paste box):\n%s\n", link)
			return nil
		},
	}
}

func timeOrDash(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format("2006-01-02")
}

func quotaStr(b *int64) string {
	if b == nil {
		return "unlimited"
	}
	return humanBytes(*b)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for n2 := n / unit; n2 >= unit; n2 /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}
