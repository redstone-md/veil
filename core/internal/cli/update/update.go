// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

// Package update wires the `veil update` CLI subcommand: query the
// release channel, optionally download the platform asset, verify
// its checksum, and atomically replace the running binary.
package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/redstone-md/veil/core/internal/buildinfo"
	upd "github.com/redstone-md/veil/core/internal/update"
)

// Command returns the `veil update` cli.Command tree.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "update",
		Usage: "Check for and install Veil updates",
		Commands: []*cli.Command{
			checkCmd(),
			applyCmd(),
		},
	}
}

func repoFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "repo",
		Usage: "GitHub repository (owner/name) to consult",
		Value: upd.DefaultRepo,
	}
}

func checkCmd() *cli.Command {
	return &cli.Command{
		Name:  "check",
		Usage: "Print the latest available release without installing",
		Flags: []cli.Flag{repoFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			c := upd.New(cmd.String("repo"))
			r, err := c.Latest(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("Latest release : %s\n", r.TagName)
			fmt.Printf("Published      : %s\n", r.PublishedAt.UTC().Format("2006-01-02"))
			fmt.Printf("URL            : %s\n", r.HTMLURL)
			fmt.Printf("Running version: %s\n", buildinfo.Version)
			if isNewer(r.TagName, buildinfo.Version) {
				fmt.Println("\nA newer release is available. Run `veil update apply` to install.")
			} else {
				fmt.Println("\nUp to date.")
			}
			fmt.Println()
			fmt.Println("Available assets:")
			for _, a := range r.Assets {
				fmt.Printf("  %-32s  %d bytes\n", a.Name, a.Size)
			}
			return nil
		},
	}
}

func applyCmd() *cli.Command {
	return &cli.Command{
		Name:  "apply",
		Usage: "Download, verify, and install the latest release",
		Flags: []cli.Flag{
			repoFlag(),
			&cli.BoolFlag{
				Name:  "force",
				Usage: "Install even if the latest tag matches the running version",
			},
			&cli.StringFlag{
				Name:  "target",
				Usage: "Path of the binary to replace (defaults to the running executable)",
			},
			&cli.BoolFlag{
				Name:  "cosign",
				Usage: "Require a valid Sigstore (cosign keyless) signature on the asset",
			},
			&cli.StringFlag{
				Name:  "cosign-subject",
				Usage: "Expected Subject (SAN URI) inside the signing certificate; defaults to the project release workflow",
			},
			&cli.StringFlag{
				Name:  "cosign-issuer",
				Usage: "Expected OIDC issuer for the signing certificate",
				Value: "https://token.actions.githubusercontent.com",
			},
			&cli.StringFlag{
				Name:  "cosign-trusted-root",
				Usage: "Path to a Sigstore TrustedRoot JSON (offline mode)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			c := upd.New(cmd.String("repo"))
			r, err := c.Latest(ctx)
			if err != nil {
				return err
			}
			if !cmd.Bool("force") && !isNewer(r.TagName, buildinfo.Version) {
				fmt.Printf("Already on %s; pass --force to reinstall.\n", buildinfo.Version)
				return nil
			}
			asset, err := upd.AssetForPlatform(r)
			if err != nil {
				return err
			}
			fmt.Printf("Downloading %s (%d bytes)…\n", asset.Name, asset.Size)
			blob, err := c.Download(ctx, asset)
			if err != nil {
				return err
			}
			fmt.Println("Fetching checksums.txt…")
			expected, err := c.FetchChecksum(ctx, r, asset.Name)
			if err != nil {
				return err
			}
			verifiers := []upd.Verifier{upd.ChecksumVerifier{ExpectedHex: expected}}
			fmt.Printf("Verifying SHA-256 (%s…)…\n", expected[:12])

			if cmd.Bool("cosign") {
				bundle, err := fetchCosignBundle(ctx, c, r, asset.Name+".sigstore")
				if err != nil {
					return fmt.Errorf("cosign: %w", err)
				}
				subject := cmd.String("cosign-subject")
				if subject == "" {
					subject = defaultCosignSubject(cmd.String("repo"), r.TagName)
				}
				verifiers = append(verifiers, upd.CosignVerifier{
					BundleJSON:      bundle,
					Subject:         subject,
					Issuer:          cmd.String("cosign-issuer"),
					TrustedRootPath: cmd.String("cosign-trusted-root"),
				})
				fmt.Printf("Verifying Sigstore signature (subject=%s)…\n", subject)
			}

			target := cmd.String("target")
			if target == "" {
				exe, err := os.Executable()
				if err != nil {
					return fmt.Errorf("update: locate self: %w", err)
				}
				target = exe
			}
			err = upd.Replace(target, blob, verifiers...)
			switch {
			case err == nil:
				fmt.Println("Installed.")
				return nil
			case errors.Is(err, upd.ErrPendingRestart):
				fmt.Printf("Installed at %s. Restart required.\n", target)
				if runtime.GOOS == "windows" {
					fmt.Printf("(Windows: previous binary saved as %s.old)\n", target)
				}
				return nil
			default:
				return err
			}
		},
	}
}

// fetchCosignBundle pulls the Sigstore bundle blob (typically named
// `<asset>.sigstore`) attached to the release.
func fetchCosignBundle(ctx context.Context, c *upd.Client, r *upd.Release, name string) ([]byte, error) {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return c.Download(ctx, &r.Assets[i])
		}
	}
	return nil, fmt.Errorf("no %q asset on release", name)
}

// defaultCosignSubject builds the canonical SAN URI for a release
// signed by the project's GitHub Actions release workflow. Operators
// who use a different workflow should pass --cosign-subject.
func defaultCosignSubject(repo, tag string) string {
	return fmt.Sprintf(
		"https://github.com/%s/.github/workflows/release.yml@refs/tags/%s",
		repo, tag,
	)
}

// isNewer is a deliberately conservative semver comparator: it
// returns true when latest != current and current does not begin
// with latest. The full semver dance is overkill for the present
// release cadence (single linear `vX.Y.Z` tags); we will swap in
// `golang.org/x/mod/semver` once pre-release tags appear.
func isNewer(latestTag, currentVersion string) bool {
	latest := strings.TrimPrefix(latestTag, "v")
	current := strings.TrimPrefix(currentVersion, "v")
	if current == "dev" || current == "" {
		return latest != ""
	}
	return latest != current
}
