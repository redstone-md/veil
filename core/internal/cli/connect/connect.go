// Package connect wires the `veil connect` subcommand: load a client
// configuration, dial the configured server over QUIC, perform the
// Noise XK initiator role, and (in v0) exchange one encrypted hello
// message as a smoke test.
package connect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/urfave/cli/v3"

	"github.com/redstone-md/veil/core/internal/config"
	"github.com/redstone-md/veil/core/internal/crypto"
	"github.com/redstone-md/veil/core/internal/session"
	"github.com/redstone-md/veil/core/internal/transport/quictr"
)

// Command returns the `veil connect` cli.Command.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "connect",
		Usage: "Connect to a Veil server (v0 smoke-test client)",
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

	// v0 smoke-test exchange.
	hello := []byte("veil-client: hello")
	cipher, err := established.Send.Encrypt(nil, nil, hello)
	if err != nil {
		return fmt.Errorf("encrypt hello: %w", err)
	}
	if _, err := conn.Write(cipher); err != nil {
		return fmt.Errorf("write hello: %w", err)
	}

	const maxMsg = 64 * 1024
	buf := make([]byte, maxMsg)
	n, err := conn.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read reply: %w", err)
	}
	if n == 0 {
		return errors.New("read reply: empty response")
	}
	plaintext, err := established.Recv.Decrypt(nil, nil, buf[:n])
	if err != nil {
		return fmt.Errorf("decrypt reply: %w", err)
	}
	slog.Info("server reply received", "payload", string(plaintext))
	return nil
}
