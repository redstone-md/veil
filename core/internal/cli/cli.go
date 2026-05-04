// Package cli wires the top-level command tree for the veil binary.
//
// Subcommands are kept thin: each is a small adapter that parses flags
// and calls into the corresponding internal package.
package cli

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/redstone-md/veil/core/internal/buildinfo"
	cliadmin "github.com/redstone-md/veil/core/internal/cli/admin"
	cliconnect "github.com/redstone-md/veil/core/internal/cli/connect"
	cliserve "github.com/redstone-md/veil/core/internal/cli/serve"
	cliupdate "github.com/redstone-md/veil/core/internal/cli/update"
	cliuser "github.com/redstone-md/veil/core/internal/cli/user"
)

// NewApp constructs the CLI application with all subcommands registered.
func NewApp(version string) *cli.Command {
	return &cli.Command{
		Name:    "veil",
		Usage:   "Self-hosted, censorship-resistant VPN",
		Version: version,
		Commands: []*cli.Command{
			cliserve.Command(),
			cliconnect.Command(),
			cliuser.Command(),
			cliadmin.Command(),
			cliupdate.Command(),
			versionCommand(),
		},
		EnableShellCompletion: true,
		Suggest:               true,
	}
}

func versionCommand() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "Print version, commit, and build date",
		Action: func(_ context.Context, _ *cli.Command) error {
			fmt.Printf("veil %s\n", buildinfo.Version)
			fmt.Printf("  commit: %s\n", buildinfo.Commit)
			fmt.Printf("  date:   %s\n", buildinfo.Date)
			return nil
		},
	}
}
