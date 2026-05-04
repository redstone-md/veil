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
	"errors"
	"fmt"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/redstone-md/veil/core/internal/admin"
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

			srv, err := admin.New(admin.Config{
				Addr:  cmd.String("addr"),
				Store: s,
			})
			if err != nil {
				return err
			}
			return srv.Run(ctx)
		},
	}
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
